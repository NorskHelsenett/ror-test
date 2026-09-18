package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/NorskHelsenett/ror-test/internal/e2e"
)

func main() { os.Exit(execute()) }

func execute() int {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: e2e run|compare|inspect-candidate|verify-report [flags]")
		return 2
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	var err error
	switch os.Args[1] {
	case "run":
		err = run(ctx, os.Args[2:])
	case "compare":
		err = compare(os.Args[2:])
	case "inspect-candidate":
		err = inspectCandidate(os.Args[2:])
	case "verify-report":
		err = verifyReport(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	suitePath := flags.String("suite", "testenv/scenarios/acl.json", "scenario suite")
	target := flags.String("target", "http://127.0.0.1:10000", "loopback API origin")
	label := flags.String("label", "current", "version label (no secrets)")
	output := flags.String("out", "artifacts/current.json", "JSON report")
	container := flags.Bool("container", false, "allow isolated Compose service names")
	oidc := flags.String("oidc", "", "test OIDC origin; obtain synthetic tokens")
	ready := flags.String("ready", "", "readiness URL on same host as target")
	if err := flags.Parse(args); err != nil {
		return err
	}
	suite, err := e2e.LoadSuite(*suitePath)
	if err != nil {
		return err
	}
	origin, err := e2e.ValidateTarget(*target, *container)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	if *ready != "" {
		endpoint, err := url.Parse(*ready)
		if err != nil || endpoint.Hostname() != origin.Hostname() || endpoint.Scheme != origin.Scheme || endpoint.User != nil {
			return fmt.Errorf("readiness URL must use the target host and scheme")
		}
		if err := waitReady(ctx, client, *ready); err != nil {
			return err
		}
	}
	variables := make(map[string]string)
	for _, name := range []string{"ADMIN_TOKEN", "READER_TOKEN", "OUTSIDER_TOKEN"} {
		if value := os.Getenv(name); value != "" {
			variables[name] = value
		}
	}
	if *oidc != "" {
		issuer, err := url.Parse(*oidc)
		if err != nil || issuer.User != nil || issuer.RawQuery != "" || issuer.Fragment != "" || issuer.Path != "" {
			return fmt.Errorf("invalid test OIDC origin")
		}
		if !(*container && issuer.Hostname() == "oidc" && issuer.Scheme == "http") {
			if _, err := e2e.ValidateTarget(*oidc, false); err != nil {
				return err
			}
		}
		for name, identity := range map[string]string{"ADMIN_TOKEN": "admin", "READER_TOKEN": "reader", "OUTSIDER_TOKEN": "outsider"} {
			token, err := testToken(ctx, client, *oidc+"/default/token", identity)
			if err != nil {
				return err
			}
			variables[name] = token
		}
	}
	report, err := e2e.Run(ctx, suite, *target, *label, variables, *container)
	if err != nil {
		return err
	}
	if err := writeJSON(*output, report); err != nil {
		return err
	}
	if err := writeJUnit(strings.TrimSuffix(*output, filepath.Ext(*output))+".xml", suite.Name, report); err != nil {
		return err
	}
	fmt.Printf("%s: %d/%d steps, report %s\n", *label, len(report.Results), len(suite.Steps), *output)
	if !report.Passed(len(suite.Steps)) {
		return fmt.Errorf("scenario assertions failed; inspect report")
	}
	return nil
}

func waitReady(ctx context.Context, client *http.Client, endpoint string) error {
	ctx, cancel := context.WithTimeout(ctx, 180*time.Second)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return fmt.Errorf("invalid readiness URL")
		}
		response, err := client.Do(request)
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("readiness deadline exceeded")
		case <-ticker.C:
		}
	}
}

func testToken(ctx context.Context, client *http.Client, endpoint, identity string) (string, error) {
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {identity}, "client_secret": {"synthetic-only"}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("invalid token endpoint")
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("test issuer unavailable")
	}
	defer response.Body.Close()
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if response.StatusCode != 200 {
		return "", fmt.Errorf("test issuer returned %d", response.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1024*1024)).Decode(&token); err != nil || token.AccessToken == "" {
		return "", fmt.Errorf("test issuer returned no access token")
	}
	return token.AccessToken, nil
}

func compare(args []string) error {
	flags := flag.NewFlagSet("compare", flag.ContinueOnError)
	baseline := flags.String("baseline", "", "baseline JSON report")
	candidate := flags.String("candidate", "", "candidate JSON report")
	output := flags.String("out", "artifacts/diff.json", "difference report")
	if err := flags.Parse(args); err != nil {
		return err
	}
	var before, after e2e.Report
	for _, input := range []struct {
		path   string
		report *e2e.Report
	}{{*baseline, &before}, {*candidate, &after}} {
		data, err := os.ReadFile(input.path)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(data, input.report); err != nil {
			return err
		}
	}
	differences := e2e.Compare(before, after)
	if err := writeJSON(*output, differences); err != nil {
		return err
	}
	fmt.Printf("comparison: %d differences\n", len(differences))
	if len(differences) > 0 {
		return fmt.Errorf("comparison failed; inspect %s", *output)
	}
	return nil
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeArtifact(path, append(data, '\n'))
}

func writeArtifact(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := file.Chmod(0600); err != nil {
		return err
	}
	_, err = file.Write(data)
	return err
}

func writeJUnit(path, name string, report e2e.Report) error {
	type failure struct {
		Message string `xml:"message,attr"`
	}
	type testCase struct {
		Name    string   `xml:"name,attr"`
		Time    string   `xml:"time,attr"`
		Failure *failure `xml:"failure,omitempty"`
	}
	suite := struct {
		XMLName  xml.Name   `xml:"testsuite"`
		Name     string     `xml:"name,attr"`
		Tests    int        `xml:"tests,attr"`
		Failures int        `xml:"failures,attr"`
		Cases    []testCase `xml:"testcase"`
	}{Name: name, Tests: len(report.Results)}
	for _, result := range report.Results {
		entry := testCase{Name: result.Name, Time: fmt.Sprintf("%.3f", float64(result.DurationMS)/1000)}
		if len(result.Failures) > 0 {
			entry.Failure = &failure{Message: strings.Join(result.Failures, "; ")}
			suite.Failures++
		}
		suite.Cases = append(suite.Cases, entry)
	}
	data, err := xml.MarshalIndent(suite, "", "  ")
	if err != nil {
		return err
	}
	return writeArtifact(path, append([]byte(xml.Header), data...))
}

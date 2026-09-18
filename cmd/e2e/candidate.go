package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/NorskHelsenett/ror-test/internal/e2e"
)

func inspectCandidate(args []string) error {
	flags := flag.NewFlagSet("inspect-candidate", flag.ContinueOnError)
	archive := flags.String("archive", "", "uncompressed OCI archive")
	checksum := flags.String("checksum", "", "expected sha256: archive checksum")
	digest := flags.String("digest", "", "expected sha256: root index or manifest digest")
	platform := flags.String("platform", "", "linux/amd64 or linux/arm64")
	output := flags.String("out", "", "candidate provenance JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	candidate, err := e2e.InspectCandidate(*archive, *checksum, *digest, *platform)
	if err != nil {
		return err
	}
	if *output == "" {
		return fmt.Errorf("candidate provenance output is required")
	}
	if err := writeJSON(*output, candidate); err != nil {
		return err
	}
	fmt.Printf("%s %s\n", candidate.ManifestDigest, candidate.ConfigDigest)
	return nil
}

func verifyReport(args []string) error {
	flags := flag.NewFlagSet("verify-report", flag.ContinueOnError)
	suitePath := flags.String("suite", "testenv/scenarios/acl.json", "expected scenario suite")
	reportPath := flags.String("report", "", "completed report")
	output := flags.String("out", "", "verified report evidence JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	suite, err := e2e.LoadSuite(*suitePath)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(*reportPath)
	if err != nil {
		return err
	}
	var report e2e.Report
	if err := json.Unmarshal(data, &report); err != nil {
		return err
	}
	if err := e2e.VerifyReport(suite, report); err != nil {
		return err
	}
	if *output == "" {
		return fmt.Errorf("verified report output is required")
	}
	digest := sha256.Sum256(data)
	return writeJSON(*output, struct {
		Schema       int    `json:"schema"`
		Suite        string `json:"suite"`
		ReportDigest string `json:"reportDigest"`
		Passed       int    `json:"passed"`
	}{1, report.Suite, "sha256:" + hex.EncodeToString(digest[:]), len(report.Results)})
}

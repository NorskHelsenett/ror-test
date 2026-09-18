package main

import (
	"fmt"

	"github.com/NorskHelsenett/ror/pkg/helpers/stringhelper"
	"github.com/biter777/countries"
)

func main() {

	test1 := countries.BY.Info()

	fmt.Println(test1)

	//stringhelper.PrettyprintStruct(test1)

	test := countries.ByNumeric(744)
	stringhelper.PrettyprintStruct(test)

	info := test.Info()
	fmt.Printf("Country name in english: %v\n", info.Name)
	fmt.Printf("Country ISO-3166 digit code: %d\n", info.Code)
	fmt.Printf("Country ISO-3166 Alpha-2 code: %v\n", info.Alpha2)
	fmt.Printf("Country ISO-3166 Alpha-3 code: %v\n", info.Alpha3)
	fmt.Printf("Country IOC/NOC code: %v\n", info.IOC)
	fmt.Printf("Country FIFA code: %v\n", info.FIFA)
	fmt.Printf("Country FIPS code: %v\n", info.FIPS)
	fmt.Printf("Country Capital: %v\n", info.Capital)
	fmt.Printf("Country ITU-T E.164 call code: %v\n", info.CallCodes)
	fmt.Printf("Country ccTLD domain: %v\n", info.Domain)
	fmt.Printf("Country UN M.49 region name: %v\n", info.Region)
	fmt.Printf("Country UN M.49 region code: %d\n", info.Region)
	fmt.Printf("Country emoji/flag: %v\n", info.Emoji)
	fmt.Printf("Country ISO-4217 Currency name in english: %v\n", info.Currency)
	fmt.Printf("Country ISO-4217 Currency digit code: %d\n", info.Currency)
	fmt.Printf("Country ISO-4217 Currency Alpha code: %v\n", info.Currency.Alpha())
	fmt.Printf("Country Subdivisions: %v\n", info.Subdivisions)
}

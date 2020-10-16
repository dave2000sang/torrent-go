package utils

import(
	"log"
)

// CheckFatal throws log.Fatal
func CheckFatal(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

// CheckPrintln prints error to log stdout
func CheckPrintln(err error) {
	if err != nil {
		log.Println(err)
	}
}
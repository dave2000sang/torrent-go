package utils

import (
	"log"
	"math"
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

// DivisionCeil returns Ceil(a/b)
func DivisionCeil(a, b int) int {
	return int(math.Ceil(float64(a) / float64(b)))
}

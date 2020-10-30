package utils

import (
	"log"
	"math"
	"os"
	// "net"
)

// CheckFatal throws log.Fatal
func CheckFatal(err error) {
	if err != nil {
		log.Fatal(err)
	}
}


// CheckPrintln prints error to log stdout
func CheckPrintln(err error, n, size int) {
	if n != size {
		log.Printf("%d != %d bytes read\n", n, size)
	}
	if err != nil {
		log.Println(err)
	}
}

// DivisionCeil returns Ceil(a/b)
func DivisionCeil(a, b int) int {
	return int(math.Ceil(float64(a) / float64(b)))
}

// WriteFile writes data to file
func WriteFile(file string, data []byte) {
	log.Println("writing data to ", file)
	f, err := os.Create(file)
	if err != nil {
		log.Fatal(err)
	}
	f.Write(data)
	f.Close()
}
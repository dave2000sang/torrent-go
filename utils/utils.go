package utils

import (
	"log"
	"math"
	// "net"
)

// CheckFatal throws log.Fatal
func CheckFatal(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

// // ReadBuffer wraps net.Read(), reads message equal to buffer length
// func ReadBuffer(buffer []byte, conn *net.TCPConn) (int, error) {
// 	n, err := conn.Read(buffer[:])
// 	CheckPrintln(err, n, len(buffer))
// 	return n, err
// }

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

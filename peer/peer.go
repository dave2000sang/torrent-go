package peer

import (
	"bytes"
	// "io"

	"github.com/dave2000sang/torrent-go/utils"

	// "io/ioutil"
	"encoding/binary"
	"log"
	"net"
	"strconv"
	"time"
)

// Peer represents a seeder
type Peer struct {
	// PeerID string `bencode:"peer id"`	(not used if compact=1)
	IP     net.IP // 4 bytes IPv4
	Port   uint16 // 2 bytes Port
	Status status
}

type status struct {
	amChocking     bool
	amInterested   bool
	peerChocking   bool
	peerInterested bool
}

// NewPeer creates a new Peer instance
func NewPeer(ip net.IP, port uint16) (*Peer, error) {
	peer := Peer{ip, port, status{true, false, true, false}}
	return &peer, nil
}


// DoHandshake sends initial handshake with peer
func (peer *Peer) DoHandshake(infoHash, clientID [20]byte) {
	handshake := []byte{}
	handshake = append(handshake, []byte{byte(19)}...)
	handshake = append(handshake, []byte("BitTorrent protocol")...)
	handshake = append(handshake, []byte{0, 0, 0, 0, 0, 0, 0, 0}...)
	handshake = append(handshake, infoHash[:]...)
	handshake = append(handshake, clientID[:]...)

	// Create TCP connection with peer
	peerAddr := peer.IP.String() + ":" + strconv.Itoa(int(peer.Port))
	log.Println("Performing handshake with peer: ", peer.IP.String())
	tcpAddr, err := net.ResolveTCPAddr("tcp", peerAddr)
	if err != nil {
		log.Println("ResolveTCPAddr failed on ", peerAddr, ". Error: ", err)
		return
	}
	conn, err := net.DialTCP("tcp", nil, tcpAddr)
	if err != nil {
		log.Println("Failed to DialTCP on ", peerAddr, ". Error: ", err)
		return
	}
	defer conn.Close()

	_, err = conn.Write(handshake)
	if err != nil {
		log.Println("Error writing handshake: ", err)
		return
	}

	// read peer response handshake
	// response := make([]byte, 1024)
	var (
		pstrlen      int
		pstr         string
		reserved     [8]byte
		peerInfoHash [20]byte
		peerID       [20]byte
	)

	// TODO - maybe run this through a loop instead

	// pstrlen, handle keep-alive message
	buf := make([]byte, 1)
	maxAttempts, retryAttempts := 3, 0
	for {
		if retryAttempts >= maxAttempts {
			log.Println("Timeout, skipping...")
			return
		}
		n, err := conn.Read(buf[:])
		utils.CheckPrintln(err)
		log.Println("n = ", n, ", buf = ", buf[:])
		if n == 0 {
			log.Println("Retry attempt: ", retryAttempts)
			time.Sleep(60 * time.Second)
			retryAttempts++
		} else {
			pstrlen = int(buf[0])
			break
		}
	}

	// pstr
	buf = make([]byte, pstrlen)
	_, err = conn.Read(buf)
	utils.CheckPrintln(err)
	pstr = string(buf[:])
	if pstr != "BitTorrent protocol" {
		log.Println("Hmm, this peer is using protocol: ", pstr)
	}

	// reserved
	buf = make([]byte, 8)
	_, err = conn.Read(buf)
	utils.CheckPrintln(err)
	copy(reserved[:], buf)

	// info hash
	buf = make([]byte, 20)
	_, err = conn.Read(buf)
	utils.CheckPrintln(err)
	copy(peerInfoHash[:], buf)

	// info hash
	buf = make([]byte, 20)
	_, err = conn.Read(buf)
	utils.CheckPrintln(err)
	copy(peerID[:], buf)

	// Verify info hashes match (and peer id if you have it)
	// close TCP connection if they don't
	if !bytes.Equal(infoHash[:], peerInfoHash[:]) {
		log.Fatal("ERROR: response info hash does not match")
	}
	log.Println("Received handshake from peer")
	peer.handleMessages(conn)
}

// handleMessage reads messages from peer
func (peer *Peer) handleMessages(conn *net.TCPConn) {
	var msgLength [4]byte
	var payloadLength uint32
	var msgID [1]byte
	var msgPayload []byte

	// Read one message from peer
	n, err := conn.Read(msgLength[:])
	utils.CheckPrintln(err)
	payloadLength = binary.BigEndian.Uint32(msgLength[:]) - 1

	n, err = conn.Read(msgID[:])
	utils.CheckPrintln(err)

	// Read the next payloadLength bytes as msgPayload
	buf := make([]byte, payloadLength)
	n, err = conn.Read(buf)
	utils.CheckPrintln(err)
	log.Printf("payloadLength = %d, n = %d", payloadLength, n) // should be equal to payloadLength
	msgPayload = append(msgPayload, buf[:n]...)

	// TODO - continue listening for more peer messages
	log.Println("Got message from peer")
	peer.parseMessage(int(msgID[0]), msgPayload)
}

// parseMessage parses peer message, excluding empty keep-alive case
func (peer *Peer) parseMessage(messageID int, payload []byte) {
	log.Println("messageID = ", messageID)
	switch messageID {
	case 0:
		// choke
		peer.Status.peerChocking = true
	case 1:
		// unchoke
		peer.Status.peerChocking = false
	case 2:
		// interested
		peer.Status.peerInterested = true
	case 3:
		// not interested
		peer.Status.peerInterested = false
	case 4:
		// have
	case 5:
		// bitfield
	case 6:
		// request
	case 7:
		// piece
	case 8:
		// cancel
	case 9:
		// port
	default:
		log.Fatal("ERROR: received invalid messageID")
	}
}

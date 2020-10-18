package peer

import (
	"bytes"
	// "io"
	// "io/ioutil"
	"encoding/binary"
	"errors"
	"log"
	"net"
	"strconv"
	"time"

	"torrent-go/utils"
)

const MaxListenAttempts = 3

// Peer represents a seeder
type Peer struct {
	// PeerID string `bencode:"peer id"`	(not used if compact=1)
	IP         net.IP // 4 bytes IPv4
	Port       uint16 // 2 bytes Port
	Status     Status
	HavePieces []byte // bitfield of pieces this peer has
}

// Status is peer status
type Status struct {
	AmChocking     bool
	AmInterested   bool
	PeerChocking   bool
	PeerInterested bool
}

// NewPeer creates a new Peer instance
func NewPeer(ip net.IP, port uint16) (*Peer, error) {
	// Set initial status with AmInterested true
	peer := Peer{ip, port, Status{true, true, true, false}, []byte{}}
	return &peer, nil
}

// TCPConnect connects client to peer and returns the TCPConn instance
func (peer *Peer) TCPConnect() (*net.TCPConn, error) {
	peerAddr := peer.IP.String() + ":" + strconv.Itoa(int(peer.Port))
	tcpAddr, err := net.ResolveTCPAddr("tcp", peerAddr)
	if err != nil {
		log.Println("ResolveTCPAddr failed on ", peerAddr)
		return nil, err
	}
	// Create TCPConn instance
	conn, err := net.DialTCP("tcp", nil, tcpAddr)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

// DoHandshake sends initial handshake with peer
func (peer *Peer) DoHandshake(infoHash, clientID [20]byte) error {
	handshake := []byte{}
	handshake = append(handshake, []byte{byte(19)}...)
	handshake = append(handshake, []byte("BitTorrent protocol")...)
	handshake = append(handshake, []byte{0, 0, 0, 0, 0, 0, 0, 0}...)
	handshake = append(handshake, infoHash[:]...)
	handshake = append(handshake, clientID[:]...)

	// Create TCP connection with peer
	log.Println("Performing handshake with peer: ", peer.IP.String())
	conn, err := peer.TCPConnect()
	if err != nil {
		return err
	}
	defer conn.Close()

	_, err = conn.Write(handshake)
	if err != nil {
		log.Println("Error sending handshake")
		conn.Close()
		return err
	}

	// read peer response handshake
	var (
		pstrlen      int
		pstr         string
		reserved     [8]byte
		peerInfoHash [20]byte
		peerID       [20]byte
	)

	// pstrlen, handle keep-alive message
	buf := make([]byte, 1)
	maxAttempts, retryAttempts := 3, 0
	for {
		if retryAttempts >= maxAttempts {
			conn.Close()
			return errors.New("Timeout, skipping")
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

	// peer ID
	buf = make([]byte, 20)
	_, err = conn.Read(buf)
	utils.CheckPrintln(err)
	copy(peerID[:], buf)

	// Verify info hashes match (and peer id if you have it)
	// close TCP connection if they don't
	if !bytes.Equal(infoHash[:], peerInfoHash[:]) {
		conn.Close()
		return errors.New("ERROR: response info hash does not match")
	}
	log.Println("Received handshake from peer")
	err = peer.HandleMessages(conn, 0, nil)
	if err != nil {
		conn.Close()
		return err
	}
	return nil
}

// HandleMessages reads messages from peer
func (peer *Peer) HandleMessages(conn *net.TCPConn, listenAttempt int, requestMsg []byte) error {
	if listenAttempt >= MaxListenAttempts {
		return errors.New("Max attempts exceeded reading messages skipping")
	}
	var (
		msgLength     [4]byte
		payloadLength uint32
		msgID         [1]byte
		msgPayload    []byte
	)
	// Listen for messages until time out
	maxAttempts, retryAttempts, delayDuration := 3, 0, time.Duration(30)
	for {
		if retryAttempts >= maxAttempts {
			return errors.New("Timeout listening for message")
		}
		n, err := conn.Read(msgLength[:])
		utils.CheckPrintln(err)
		if n == 0 {
			log.Println("Keep alive for peer message attempt: ", retryAttempts)
			time.Sleep(delayDuration * time.Second)
			retryAttempts++
		} else {
			payloadLength = binary.BigEndian.Uint32(msgLength[:]) - 1
			break
		}
	}
	n, err := conn.Read(msgID[:])
	utils.CheckPrintln(err)

	// Read the next payloadLength bytes as msgPayload
	buf := make([]byte, payloadLength)
	n, err = conn.Read(buf)
	utils.CheckPrintln(err)
	msgPayload = append(msgPayload, buf[:n]...)

	log.Println("Got message from peer")

	err = peer.parseMessage(int(msgID[0]), msgPayload, requestMsg)
	if err != nil {
		return err
	}
	// Return if peer unchokes
	if !peer.Status.PeerChocking {
		log.Println("Peer unchocked me!")
		return nil
	}
	// continue listening for messages until unchokes
	return peer.HandleMessages(conn, listenAttempt+1, requestMsg)
}

// parseMessage parses peer message, excluding empty keep-alive case
func (peer *Peer) parseMessage(messageID int, payload []byte, requestMsg []byte) error {
	log.Println("messageID = ", messageID)
	switch messageID {
	case 0: // choke
		peer.Status.PeerChocking = true
	case 1: // unchoke
		peer.Status.PeerChocking = false
	case 2: // interested
		peer.Status.PeerInterested = true
	case 3: // not interested
		peer.Status.PeerInterested = false
	case 4: // have
		pieceIndex := int(binary.BigEndian.Uint32(payload))
		byteIndex := pieceIndex / 8
		// update HavePieces
		peer.HavePieces[byteIndex] = updateBitfield(pieceIndex, peer.HavePieces[byteIndex])
	case 5: // bitfield
		if len(payload) != len(peer.HavePieces) {
			return errors.New("Peer bitfield message length mismatch")
		}
		peer.HavePieces = payload
	case 6: // request
		// TODO - implement me
		return errors.New("Peer bitfield message length mismatch")
	case 7: // piece
		// Should not receive a piece message here in first message
		if requestMsg == nil {
			return errors.New("Received PIECE message without requesting for it")
		}
		requestPieceBody := requestMsg[5:]
		if !bytes.Equal(payload, requestPieceBody) {
			return errors.New("ERROR: Peer sent piece doesn't match requested piece")
		}
		pieceIndex := payload[:4]
		pieceBegin := payload[4:8]
		pieceBlock := payload[8:]
		// TODO - Save block to Piece struct

		
	case 8: // cancel
		// TODO - implement me
		return errors.New("Received CANCEL message from peer, skipping")
	case 9: // port
		// TODO - implement me
		return errors.New("Received PORT message from peer, skipping")
	default:
		return errors.New("Invalid message ID, skipping")
	}
	return nil
}

// updateBitfield returns updated bitfield value with index bit set to 1
func updateBitfield(index int, oldBitfield byte) byte {
	// get corresponding index in byte
	bitIndex := index % 8
	return oldBitfield | (1 << bitIndex)
}

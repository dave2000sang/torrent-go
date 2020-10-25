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
	"torrent-go/piece"
	"torrent-go/utils"
)

// MaxListenAttempts before timing out
const MaxListenAttempts = 3

// Peer represents a seeder
type Peer struct {
	// PeerID string `bencode:"peer id"`	(not used if compact=1)
	IP         net.IP // 4 bytes IPv4
	Port       uint16 // 2 bytes Port
	Status     Status
	HavePieces []byte       // bitfield of pieces this peer has
	Connection *net.TCPConn // TCP connection instance
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
	peer := Peer{ip, port, Status{true, false, true, false}, []byte{}, nil}
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
func (peer *Peer) DoHandshake(infoHash, clientID [20]byte) (*net.TCPConn, error) {
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
		return conn, err
	}
	// defer conn.Close()

	_, err = conn.Write(handshake)
	if err != nil {
		log.Println("Error sending handshake")
		return conn, err
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
			return conn, errors.New("Timeout, skipping")
		}
		n, err := conn.Read(buf[:])
		utils.CheckPrintln(err, n, len(buf))
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
	n, err := conn.Read(buf)
	utils.CheckPrintln(err, n, len(buf))
	pstr = string(buf[:])
	if pstr != "BitTorrent protocol" {
		log.Println("Hmm, this peer is using protocol: ", pstr)
	}

	// reserved
	buf = make([]byte, 8)
	n, err = conn.Read(buf)
	utils.CheckPrintln(err, n, len(buf))
	copy(reserved[:], buf)

	// info hash
	buf = make([]byte, 20)
	n, err = conn.Read(buf)
	utils.CheckPrintln(err, n, len(buf))
	copy(peerInfoHash[:], buf)

	// peer ID
	buf = make([]byte, 20)
	n, err = conn.Read(buf)
	utils.CheckPrintln(err, n, len(buf))
	copy(peerID[:], buf)

	// Verify info hashes match (and peer id if you have it)
	// close TCP connection if they don't
	if !bytes.Equal(infoHash[:], peerInfoHash[:]) {
		return conn, errors.New("ERROR: response info hash does not match")
	}
	log.Println("Received handshake from peer")

	// Send "interested" message
	interested := make([]byte, 5)
	binary.BigEndian.PutUint32(interested[:4], 1)
	copy(interested[4:5], []byte{2})

	_, err = conn.Write(interested)
	if err != nil {
		return conn, err
	}
	peer.Status.AmInterested = true
	log.Println("Sent interested msg")

	// Listen for messages until peer unchokes
	listenAttempt := 0
	for {
		log.Println("listen unchoke attempt: ", listenAttempt)
		if listenAttempt >= MaxListenAttempts {
			return conn, errors.New("Max attempts exceeded while reading messages, skipping")
		}
		msgID, msgPayload, err := peer.ReadMessage(conn)
		if err != nil {
			return conn, err
		}
		err = peer.HandleMessage(msgID, msgPayload, nil)
		if err != nil {
			return conn, err
		}
		// Break if peer unchokes
		if !peer.Status.PeerChocking {
			log.Println("Peer unchocked me!")
			break
		}
		listenAttempt++
	}
	return conn, nil
}

// ReadMessage reads messages from peer and returns message ID and payload
func (peer *Peer) ReadMessage(conn *net.TCPConn) (int, []byte, error) {
	var (
		msgLength     [4]byte
		payloadLength uint32
		msgID         [1]byte
		msgPayload    []byte
	)
	// Listen for messages until time out
	maxAttempts, retryAttempts, delayDuration := 3, 0, time.Duration(15)
	for {
		if retryAttempts >= maxAttempts {
			return 0, nil, errors.New("Timeout listening for message")
		}
		n, err := conn.Read(msgLength[:])
		if err != nil {
			return 0, nil, err
		}
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
	utils.CheckPrintln(err, n, len(msgID))
	log.Println("msgID: ", int(msgID[0]))

	// Read the next payloadLength bytes as msgPayload
	buf := make([]byte, payloadLength)
	n, err = conn.Read(buf)
	utils.CheckPrintln(err, n, len(buf))
	msgPayload = append(msgPayload, buf[:n]...)

	log.Println("Received message from peer")
	return int(msgID[0]), msgPayload, nil
}

// HandleMessage handles initial peer message(s)
func (peer *Peer) HandleMessage(messageID int, payload []byte, requestMsg []byte) error {
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
		return errors.New("Received unexpected REQUEST message from peer, skipping")
	case 7: // piece
		// Should not receive a piece message here in first message
		return errors.New("Received unexpected PIECE message from peer, skipping")
	case 8: // cancel
		// TODO - implement me
		return errors.New("Received unexpected CANCEL message from peer, skipping")
	case 9: // port
		// TODO - implement me
		return errors.New("Received unexpected PORT message from peer, skipping")
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

// HasPiece returns true if peer has this piece (using bitfield)
func (peer Peer) HasPiece(pieceIndex int) bool {
	byteIndex := pieceIndex / 8
	byteOffset := pieceIndex % 8
	return peer.HavePieces[byteIndex]&(1<<byteOffset) != 0
}

// GetNextPiece finds the next incomplete piece that peer owns
func (peer *Peer) GetNextPiece(pieceList []*piece.Piece) (int, int, *piece.Piece) {
	for _, piece := range pieceList {
		if !piece.IsComplete {
			// Check that peer has the piece
			pieceIndex := piece.Index
			if peer.HasPiece(pieceIndex) {
				return pieceIndex, piece.BlockIndex, piece
			}
		}
	}
	return -1, 0, nil
}

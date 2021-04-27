package peer

import (
	"bytes"
	"io"

	// "io/ioutil"
	"encoding/binary"
	"errors"
	"log"
	"net"
	"strconv"
	"time"
	"torrent-go/utils"
)

// MaxListenAttempts before timing out
const MaxListenAttempts = 100

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
func NewPeer(ip net.IP, port uint16, numPieces int) (*Peer, error) {
	// Set initial status with AmInterested true
	peer := Peer{ip, port, Status{true, false, true, false}, []byte{}, nil}
	peer.HavePieces = make([]byte, utils.DivisionCeil(numPieces, 8))
	return &peer, nil
}

func (peer *Peer) String() string {
	return peer.IP.String() + ":" + strconv.Itoa(int(peer.Port))
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
func (peer *Peer) DoHandshake(infoHash, clientID [20]byte, UseDHT bool) error {
	handshake := []byte{}
	handshake = append(handshake, []byte{byte(19)}...)
	handshake = append(handshake, []byte("BitTorrent protocol")...)
	if UseDHT {
		// Set last reserved bit for DHT
		handshake = append(handshake, []byte{0, 0, 0, 0, 0, 0, 0, 1}...)
	} else {
		handshake = append(handshake, []byte{0, 0, 0, 0, 0, 0, 0, 0}...)
	}
	handshake = append(handshake, infoHash[:]...)
	handshake = append(handshake, clientID[:]...)

	// Create TCP connection with peer
	log.Println("Performing handshake with peer: ", peer)
	conn, err := peer.TCPConnect()
	peer.Connection = conn
	if err != nil {
		return err
	}

	_, err = conn.Write(handshake)
	if err != nil {
		log.Println("Error sending handshake")
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
	maxAttempts, retryAttempts := 2, 0
	for {
		if retryAttempts >= maxAttempts {
			return errors.New("timeout, skipping")
		}
		n, err := io.ReadFull(conn, buf)
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
	n, err := io.ReadFull(conn, buf)
	utils.CheckPrintln(err, n, len(buf))
	pstr = string(buf[:])
	if pstr != "BitTorrent protocol" {
		log.Println("Hmm, this peer is using protocol: ", pstr)
	}

	// reserved
	buf = make([]byte, 8)
	n, err = io.ReadFull(conn, buf)
	utils.CheckPrintln(err, n, len(buf))
	copy(reserved[:], buf)

	// info hash
	buf = make([]byte, 20)
	n, err = io.ReadFull(conn, buf)
	utils.CheckPrintln(err, n, len(buf))
	copy(peerInfoHash[:], buf)

	// peer ID
	buf = make([]byte, 20)
	n, err = io.ReadFull(conn, buf)
	utils.CheckPrintln(err, n, len(buf))
	copy(peerID[:], buf)

	// Verify info hashes match (and peer id if you have it)
	// close TCP connection if they don't
	if !bytes.Equal(infoHash[:], peerInfoHash[:]) {
		return errors.New("ERROR: response info hash does not match")
	}
	log.Println("Received handshake from peer", peer)

	
	// Send "interested" message
	interested := make([]byte, 5)
	binary.BigEndian.PutUint32(interested[:4], 1)
	copy(interested[4:5], []byte{2})

	_, err = conn.Write(interested)
	if err != nil {
		return err
	}
	peer.Status.AmInterested = true
	// log.Println("Sent interested msg")

	// Listen for messages until peer unchokes
	listenAttempt := 0
	for {
		// log.Println("listen unchoke attempt: ", listenAttempt)
		if listenAttempt >= MaxListenAttempts {
			return errors.New("max attempts exceeded while reading messages, skipping")
		}
		msgID, msgPayload, err := peer.ReadMessage(conn)
		if err != nil {
			return err
		}
		err = peer.HandleMessage(msgID, msgPayload, nil)
		if err != nil {
			return err
		}
		// Break if peer unchokes
		if !peer.Status.PeerChocking {
			log.Printf("Peer [%s] unchocked me!\n", peer.IP.String())
			break
		}
		listenAttempt++
	}
	return nil
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
			return 0, nil, errors.New("timeout listening for message")
		}
		n, err := io.ReadFull(conn, msgLength[:])
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
	n, err := io.ReadFull(conn, msgID[:])
	utils.CheckPrintln(err, n, len(msgID))
	// log.Println("msgID: ", int(msgID[0]))

	// Message without payload (e.g. choke, unchoke, interested)
	if payloadLength == 0 {
		return int(msgID[0]), msgPayload, nil 
	}
	// Read the next payloadLength bytes as msgPayload
	buf := make([]byte, payloadLength)
	n, err = io.ReadFull(conn, buf)
	utils.CheckPrintln(err, n, len(buf))
	msgPayload = append(msgPayload, buf[:n]...)

	// log.Println("Received message from peer")
	return int(msgID[0]), msgPayload, nil
}

// HandleMessage handles initial peer message(s)
func (peer *Peer) HandleMessage(messageID int, payload []byte, requestMsg []byte) error {
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
			return errors.New("peer bitfield message length mismatch")
		}
		peer.HavePieces = payload
	case 6: // request
		return errors.New("received unexpected REQUEST message from peer, skipping")
	case 7: // piece
		// Should not receive a piece message here in first message
		return errors.New("received unexpected PIECE message from peer, skipping")
	case 8: // cancel
		// TODO - implement me
		return errors.New("received unexpected CANCEL message from peer, skipping")
	case 9: // port
		// TODO - implement me
		// return errors.New("received unexpected PORT message from peer, skipping")
		log.Printf("Got PORT %d message\n", binary.BigEndian.Uint16(payload))
		return nil
	default:
		return errors.New("invalid message ID, skipping")
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

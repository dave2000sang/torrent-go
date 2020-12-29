// Implementation of KRPC protocol used in DHT

package dht

import (
	"net"
	// "time"
	"bytes"
	"errors"
	"log"
	"strconv"

	"github.com/jackpal/bencode-go"
)

// MaxPacketSize should be enough
var MaxPacketSize = 4096

// KrpcMessage is a general container for any KRPC message
type krpcMessage struct {
	MessageType   string                 `bencode:"y"`
	TransactionID string                 `bencode:"t"`
	QueryName     string                 `bencode:"q"`
	QueryBody     map[string]interface{} `bencode:"a"`
	ResponseData  responseBody           `bencode:"r"`
	// Complicated to unmarshal error of [int, string]
	// Errors        []string	  `bencode:"e"`
}

// krpcQuery used for bencode
type krpcQuery struct {
	MessageType   string                 `bencode:"y"`
	TransactionID string                 `bencode:"t"`
	QueryName     string                 `bencode:"q"`
	QueryBody     map[string]interface{} `bencode:"a"`
}

type responseBody struct {
	Values []string `bencode:"values"`
	ID     string   `bencode:"id"`
	Nodes  string   `bencode:"nodes"`
	Token  string   `bencode:"token"`
}

type packetNode struct {
	b    []byte
	addr net.UDPAddr
}

// Node is a remote DHT node that we store in routing table
type Node struct {
	ID      [20]byte
	address net.UDPAddr
	// past queries sent to this node by me, key: transaction id
	sentQueries map[string]krpcQuery
	counter     int // transaction id counter
}

// NewNode creates a new node
func NewNode(id [20]byte, addr net.UDPAddr) *Node {
	return &Node{
		ID:          id,
		address:     addr,
		sentQueries: make(map[string]krpcQuery),
		counter:     0,
	}
}

func makeQuery(msgType, transactionID, queryName string, queryBody map[string]interface{}) ([]byte, krpcQuery) {
	newQuery := krpcQuery{msgType, transactionID, queryName, queryBody}
	var buf bytes.Buffer
	bencode.Marshal(&buf, newQuery)
	return buf.Bytes(), newQuery
}

// Returns a new transaction id and updates node counter
func makeTransactionID(node *Node) string {
	node.counter += 1 % 26
	return string('a' + rune(node.counter))
}

func decodeMessage(data []byte) (krpcMessage, error) {
	response := krpcMessage{}
	var buf bytes.Buffer
	err := bencode.Unmarshal(&buf, response)
	return response, err
}

// listenSocket reads UDP packets and sends them to packetChan
func listenSocket(socket *net.UDPConn, packetChan chan packetNode) {
	for {
		b := make([]byte, MaxPacketSize)
		n, addr, err := socket.ReadFromUDP(b)
		if err != nil {
			log.Println(err)
		}
		b = b[:n]
		if n > 0 && err == nil {
			p := packetNode{b, *addr}
			packetChan <- p
		}
	}
}

//======================= Parse krpc message ====================================================
func parsePeerStr(peerStr string) Peer {
	ip := net.ParseIP(peerStr[0 : 4])
	port, _ := strconv.ParseUint(peerStr[4:6], 10, 16)
	return Peer{ip, uint16(port)}
}

// parseCompactNodes parses krpc response nodes, returning list of node pointers
func parseCompactNodes(nodeStr string) []*Node {
	nodes := make([]*Node, len(nodeStr)/26)
	for i := 0; i <= len(nodeStr)-26; i += 26 {
		nodeID := [20]byte{}
		nodeIDStr := nodeStr[i : i+20]
		copy(nodeID[:], nodeIDStr)
		ip := nodeStr[i+20 : i+24]
		port := nodeStr[i+24 : i+26]
		addr, err := net.ResolveUDPAddr("udp", ip+":"+port)
		if err != nil {
			log.Println("Warning: node ", nodeIDStr, " failed with error: ", err)
			continue
		}
		nodes = append(nodes, NewNode(nodeID, *addr))
	}
	return nodes
}

// parseInfoHash parses query argument for info hash key
func parseInfoHash(query krpcQuery) ([20]byte, error) {
	infoHash := [20]byte{}
	infoHashRaw, ok := query.QueryBody["info_hash"]
	if !ok {
		return infoHash, errors.New("Error: failed to parse info hash from query")
	}
	infoHashStr, _ := infoHashRaw.(string)
	copy(infoHash[:], infoHashStr)
	return infoHash, nil
}

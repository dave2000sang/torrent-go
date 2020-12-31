// Implementation of KRPC protocol used in DHT

package dht

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
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
	Errors        []string	  `bencode:"e"`
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
	ID          [20]byte
	address     net.UDPAddr
	sentQueries map[string]krpcQuery // stores past queries sent to this node by me, key: transaction id
	counter     int                  // transaction id counter
	lastContact time.Time            // Used for LRU cache buckets in rtable
}

// NewNode creates a new node
func NewNode(id [20]byte, addr net.UDPAddr) *Node {
	return &Node{
		ID:          id,
		address:     addr,
		sentQueries: make(map[string]krpcQuery),
		counter:     0,
		lastContact: time.Now(),
	}
}

func (node *Node) updateLRU() {
	node.lastContact = time.Now()
}

func (node *Node) String() string {
	return "Node(" + node.address.String() +")"
}

func makeQuery(msgType, transactionID, queryName string, queryBody map[string]interface{}) ([]byte, krpcQuery) {
	newQuery := krpcQuery{msgType, transactionID, queryName, queryBody}
	var buf bytes.Buffer
	bencode.Marshal(&buf, newQuery)
	return buf.Bytes(), newQuery
}

func makeResponse(msgType, transactionID string, resBody responseBody) []byte {
	res := krpcMessage{
		MessageType:   msgType,
		TransactionID: transactionID,
		ResponseData:  resBody,
	}
	var buf bytes.Buffer
	bencode.Marshal(&buf, res)
	return buf.Bytes()
}

// Returns a unique transaction id and updates node counter
func makeTransactionID(node *Node) string {
	t := string('0' + rune(node.counter))
	for {
		// t must not exist in sentQueries
		if _, ok := node.sentQueries[t]; !ok {
			break
		}
		node.counter += 1 % 77 // printable ascii character
		t = string('0' + rune(node.counter))
	}
	return t
}

func decodeMessage(data []byte) (krpcMessage, error) {
	// Catch bencode unmarshalling panics
	defer func() {
		if x := recover(); x != nil {
			log.Println("HANDLING PANIC DECODING MESSAGE", x)
			var buf bytes.Buffer
			decoded, err := bencode.Decode(&buf)
			log.Println(decoded, err)
			log.Printf("run time panic: %v\n", x)
		}
	}()
	response := krpcMessage{}
	var buf bytes.Buffer
	buf.Write(data)
	err := bencode.Unmarshal(&buf, &response)
	return response, err
}

// listenSocket reads UDP packets and sends them to packetChan, runs concurrently with dht.mainLoop()
func listenSocket(socket *net.UDPConn, packetChan chan packetNode, wg *sync.WaitGroup, stopDHT chan bool) {
	defer wg.Done()
	for {
		b := make([]byte, MaxPacketSize)
		n, addr, err := socket.ReadFromUDP(b)
		if err != nil {
			log.Println("Error listenSocket: ", err)
			continue
		}
		b = b[:n]
		if n > 0 && err == nil {
			p := packetNode{b, *addr}
			select {
			case packetChan <- p: // send packet back to packetChan
			case <-stopDHT:
				return
			}
		}
		// Return if stopDHT
		select {
		case <-stopDHT:
			return
		default:
		}
	}
}

//======================= Parse krpc message =======================
func parseUDPAddress(addrStr string) (net.IP, uint16, error) {
	if len(addrStr) != 6 {
		return nil, 0, fmt.Errorf("Error parsing addrStr %s", addrStr)
	}
	ip := net.IP([]byte(addrStr[0:4]))
	port := binary.BigEndian.Uint16([]byte(addrStr[4:6]))
	return ip, port, nil
}

func parsePeerStr(peerStr string) (Peer, error) {
	ip, port, err := parseUDPAddress(peerStr)
	if err != nil {
		return Peer{}, err
	}
	return Peer{ip, uint16(port)}, nil
}

// parseCompactNodes parses krpc response nodes, returning list of node pointers
func parseCompactNodes(nodeStr string) []*Node {
	nodes := []*Node{}
	for i := 0; i <= len(nodeStr)-26; i += 26 {
		nodeID := [20]byte{}
		nodeIDStr := nodeStr[i : i+20]
		copy(nodeID[:], nodeIDStr)
		ip, port, err := parseUDPAddress(nodeStr[i+20:i+26])
		if err != nil {
			log.Println("Error parseCompactNodes: cannot parse ", nodeStr[i+20:i+26], " UDP address: ", err)
			continue
		}
		addr, err := net.ResolveUDPAddr("udp", ip.String()+":"+strconv.Itoa(int(port)))
		if err != nil {
			log.Println("Error parseCompactNodes: cannot resolve ", nodeStr[i+20:i+26], " UDP address: ", err)
			continue
		}
		n := NewNode(nodeID, *addr)
		nodes = append(nodes, n)
	}
	return nodes
}

// parseQueryKey parses query argument for key
func parseQueryHashValue(key string, query map[string]interface{}) ([20]byte, error) {
	res := [20]byte{}
	if keyRaw, ok := query[key]; ok {
		if value, ok := keyRaw.(string); ok {
			copy(res[:], value)
			return res, nil
		}
	}
	return res, fmt.Errorf("Error: failed to find key %s from query", key)
}

// parseQueryKey parses query argument for key
func parseQueryKey(key string, query map[string]interface{}) (string, error) {
	if keyRaw, ok := query[key]; ok {
		if value, ok := keyRaw.(string); ok {
			return value, nil
		}
	}
	return "", fmt.Errorf("Error: failed to find key %s from query", key)
}

func parseQueryKeyInt(key string, query map[string]interface{}) (int, error) {
	if keyRaw, ok := query[key]; ok {
		if value, ok := keyRaw.(int); ok {
			return value, nil
		}
	}
	return -1, fmt.Errorf("Error: failed to find key %s from query", key)
}

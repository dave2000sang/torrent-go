// Distributed Hash Table implementation for finding peers
// Read more: http://www.bittorrent.org/beps/bep_0005.html

package dht

import (
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net"
	"time"
)

// DHT node instance to find peers
type DHT struct {
	Rtable         *RoutingTable
	nodeID         [20]byte
	socket         *net.UDPConn
	FoundPeers     chan Peer // return found peers through this channel
	announcedPeers []Peer    // list of peers that announced to this DHT who have infohash
}

// Peer is a bittorrent client
type Peer struct {
	IP   net.IP
	Port uint16
}

// NewDHT creates a new DHT instance
func NewDHT() *DHT {
	// Generate a random node id
	nodeID := [20]byte{}
	rand.Seed(time.Now().UnixNano())
	rand.Read(nodeID[:])
	return &DHT{
		NewRoutingTable(nodeID),
		nodeID,
		nil,
		make(chan Peer),
		[]Peer{},
	}
}

// Start running DHT node
func (dht *DHT) Start() error {
	err := dht.createServer()
	if err != nil {
		return err
	}
	go dht.mainLoop()
	return nil
}

// Mail loop function, handles all incoming/outgoing network traffic
func (dht *DHT) mainLoop() {
	defer dht.socket.Close()
	dht.bootstrap()

	packetChan := make(chan packetNode)
	go listenSocket(dht.socket, packetChan)

	for {
		select {
		case p := <-packetChan:
			err := dht.handlePacket(p)
			if err != nil {
				log.Println(err)
			}
		}
	}
}

func (dht *DHT) bootstrap() {
	bootstrapNodes := []string{
		"router.utorrent.com:6881",
		"router.bittorrent.com:6881",
		"dht.transmissionbt.com:6881",
		"router.bitcomet.com:6881",
		"dht.aelitis.com:6881",
	}
	// Initialize routing table with bootstrap nodes
	for _, bootstrapAddr := range bootstrapNodes {
		// Ping bootstrap node, inserting the node in routing table
		dht.pingAddr(bootstrapAddr)
	}
}

func (dht *DHT) createServer() error {
	// Start a UDP server to listen for messages
	addr, _ := net.ResolveUDPAddr("udp", ":2000")
	socket, err := net.ListenUDP("udp", addr)
	if err != nil {
		return err
	}
	dht.socket = socket
	log.Println("Listening UDP on ", socket.LocalAddr())
	return nil
}

// pingAddr pings the node at nodeAddr
func (dht *DHT) pingAddr(nodeAddr string) {
	node, err := dht.Rtable.getNodeOrCreate(nodeAddr, [20]byte{})
	if err != nil {
		log.Println("Failed to ping node ", nodeAddr, err)
	}
	if err = dht.pingNode(node); err != nil {
		log.Println(err)
	}
}

// Ping node for its node ID
func (dht *DHT) pingNode(node *Node) error {
	transactionID := makeTransactionID(node)
	body := map[string]interface{}{
		"id": string(dht.nodeID[:]),
	}
	queryBytes, querykrpc := makeQuery("q", transactionID, "ping", body)
	_, err := dht.socket.WriteToUDP(queryBytes, &node.address)
	if err == nil {
		node.sentQueries[transactionID] = querykrpc
	}
	return err
}

// findNode sends a DHT find_node query for target node id to node
func (dht *DHT) findNode(node *Node, targetNodeID [20]byte) error {
	transactionID := makeTransactionID(node)
	body := map[string]interface{}{
		"id":     string(dht.nodeID[:]),
		"target": string(targetNodeID[:]),
	}
	queryBytes, querykrpc := makeQuery("q", transactionID, "find_node", body)
	_, err := dht.socket.WriteToUDP(queryBytes, &node.address)
	if err == nil {
		node.sentQueries[transactionID] = querykrpc
	}
	return err
}

// TriggerGetPeers begins fetching for peers, returning peers found in a channel
func (dht *DHT) TriggerGetPeers(infoHash [20]byte) {
	// TODO: Which node to send get_peers message to? For now, just get random node
	var node *Node
	for k := range dht.Rtable.nodeMap {
		node = dht.Rtable.nodeMap[k]
		break
	}
	log.Println("Triggered getPeers on node ", string(node.ID[:]))
	err := dht.getPeers(node, infoHash)
	if err != nil {
		log.Println("FindPeers failed with error, ", err)
	}
}

// getPeers sends a DHT get_peers query to node
func (dht *DHT) getPeers(node *Node, infoHash [20]byte) error {
	transactionID := makeTransactionID(node)
	body := map[string]interface{}{
		"id":        string(dht.nodeID[:]),
		"info_hash": string(infoHash[:]),
	}
	queryBytes, querykrpc := makeQuery("q", transactionID, "info_hash", body)
	_, err := dht.socket.WriteToUDP(queryBytes, &node.address)
	if err != nil {
		return err
	}
	node.sentQueries[transactionID] = querykrpc
	return nil
}

// AnnouncePeer sends DHT announce_peer query to node
func (dht *DHT) announcePeer(node *Node, port uint16, token string, infoHash [20]byte) {

}

// handlePacket processes a received UDP packet, can be response or a query
func (dht *DHT) handlePacket(p packetNode) error {
	msg, err := decodeMessage(p.b)
	if err != nil {
		return err
	}
	if msg.MessageType == "r" {
		// Handle response message
		node, ok := dht.Rtable.nodeMap[p.addr.String()]
		if !ok {
			return errors.New("Error: response query missing node")
		}
		// Verify that message was sent to this node previously
		foundQuery, ok := node.sentQueries[msg.TransactionID]
		if !ok {
			return errors.New("Error: could not verify message history")
		}
		switch foundQuery.QueryName {
		case "ping":
			fmt.Println("Received ping response")
			// Update node ID if missing (response ping messages)
			if node.ID == [20]byte{} {
				copy(node.ID[:], []byte(msg.ResponseData.ID))
			}
		case "find_node":
			log.Println("Received find_node response")
			dht.handleFindNode(node, msg)
		case "get_peers":
			log.Println("Received get_peers response")
			dht.handleGetPeers(node, msg, foundQuery)
		case "announce_peer":
			log.Println("Received announce_peer response")
			// nothing to do
		default:
			log.Println("Error: Unknown query type")
		}

	} else if msg.MessageType == "q" {
		// This is a query from remote node
		if queryNodeID, ok := msg.QueryBody["id"]; ok {
			queryNodeID, ok := queryNodeID.(string)
			if !ok {
				return fmt.Errorf("Error: Unknown datatype query node id: %v", queryNodeID)
			}
			log.Printf("Received %s query from node id: %s\n", msg.QueryName, queryNodeID)
			// node, ok := dht.Rtable.nodeMap[p.addr.String()]
			nodeID := [20]byte{}
			copy(nodeID[:], queryNodeID)
			// Get or create the node
			node, err := dht.Rtable.getNodeOrCreate(p.addr.String(), nodeID)
			if err != nil {
				return err
			}
			// Verify node id matches (if node already exists)
			if string(node.ID[:]) != queryNodeID {
				log.Printf("Warning: query node id (%s) does not match existing node (%s)", queryNodeID, string(node.ID[:]))
				// copy(node.ID[:], []byte(queryNodeID))
			}
			switch msg.QueryName {
			case "ping":
				dht.respondPing(node, msg)
			case "find_node":
				dht.respondFindNode(node, msg)
			case "get_peers":
				dht.respondGetPeers(node, msg)
			case "announce_peer":
				dht.respondAnnouncePeer(node, msg)
			default:
				log.Println("Error: Unknown query type")
			}
		} else {
			log.Println("Error: received query from unknown node")
		}
	} else {
		// "e" error case
		log.Println("Received Error packet from ", p.addr)
		log.Println(msg)
	}
	return nil
}

//////////// Handle remote node responses

func (dht *DHT) handleFindNode(node *Node, response krpcMessage) {
	body := response.ResponseData
	if len(body.Nodes) > 0 {
		for _, closeNode := range parseCompactNodes(body.Nodes) {
			// TODO: how to handle find_nodes response
			// Add to routing table?
			dht.Rtable.insertNode(closeNode)
		}
	} else {
		log.Println("find_node responded with empty nodes")
	}
}

func (dht *DHT) handleGetPeers(node *Node, response krpcMessage, foundQuery krpcQuery) {
	body := response.ResponseData
	infoHash, err := parseInfoHash(foundQuery)
	if err != nil {
		log.Println(err)
		return
	}
	// Store token in tokenNodeMap for use later when announce_peers
	dht.Rtable.tokenNodeMap[node.address.String()] = body.Token
	// If values present, return peers to client
	if len(body.Values) > 0 {
		log.Println("get_peers response with peers")
		// Parse and return peers to client through the FoundPeers channel
		for _, peerStr := range body.Values {
			peer := parsePeerStr(peerStr)
			dht.FoundPeers <- peer
		}
	} else {
		if len(body.Nodes) > 0 {
			log.Println("get_peers response with nodes")
			// Read Nodes in response (K closest nodes to infoHash)
			for _, closeNode := range parseCompactNodes(body.Nodes) {
				// Q: Do we want to insert node into routing table?
				// node, err := dht.Rtable.getNodeOrCreate(node.address.String(), node.ID)

				// Call get_peers on node
				dht.getPeers(closeNode, infoHash)
			}
		} else {
			log.Println("get_peers response with nothing")
		}
	}
}

//////////// Respond to remote node queries

func (dht *DHT) respondPing(node *Node, response krpcMessage) {
	queryBytes, _ := makeQuery("r", response.TransactionID, "ping", map[string]interface{}{"id": string(dht.nodeID[:])})
	_, err := dht.socket.WriteToUDP(queryBytes, &node.address)
	if err != nil {
		log.Println("Failed to respond ping with error: ", err)
	}
}

func (dht *DHT) respondFindNode(node *Node, response krpcMessage) {

}

func (dht *DHT) respondGetPeers(node *Node, response krpcMessage) {

}

func (dht *DHT) respondAnnouncePeer(node *Node, response krpcMessage) {

}

////////////////////////////// Miscelaneous

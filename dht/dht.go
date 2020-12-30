// Distributed Hash Table implementation for finding peers
// Read more: http://www.bittorrent.org/beps/bep_0005.html

package dht

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net"
	"time"
	"io"
	"crypto/sha1"
	"encoding/binary"
)

// DHT node instance to find peers
type DHT struct {
	Rtable          *RoutingTable
	nodeID          [20]byte
	socket          *net.UDPConn
	FoundPeers      chan Peer         	// return found peers through this channel
	// map of info hash (str) to list of nodes who announced with the infohash
	// used for keeping track of node's owned torrents, used for announce_peer queries
	infoHashPeerContacts map[string][]Peer
	secret				string		// seed used for generating tokens
}

// Peer represents a bittorrent client
type Peer struct {
	IP   net.IP
	Port uint16
}

// Compact returns compact form of Peer (ip:port)
func (peer *Peer) Compact() string {
	compact := [6]byte{}
	copy(compact[:4], peer.IP)
	binary.BigEndian.PutUint16(compact[4:6], peer.Port)
	return string(compact[:])
}

// NewDHT creates a new DHT instance
func NewDHT() *DHT {
	// Generate a random node id
	nodeID := [20]byte{}
	rand.Seed(time.Now().UnixNano())
	rand.Read(nodeID[:])
	secret := [10]byte{}
	rand.Read(secret[:])
	return &DHT{
		Rtable: NewRoutingTable(nodeID),
		nodeID: nodeID,
		socket: nil,
		FoundPeers: make(chan Peer),
		infoHashPeerContacts: make(map[string][]Peer),
		secret: string(secret[:]),
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
	// TODO: Get token from tokenNodeMap
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
		// Check node id matches response node id
		if msg.ResponseData.ID != string(node.ID[:]) {
			return errors.New("Error: response node id mismatch")
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
				// copy(node.ID[:], []byte(queryNodeID)) // but then have to update node in rtable
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
			// Add nodes to routing table
			dht.Rtable.insertNode(closeNode)
		}
	} else {
		log.Println("find_node responded with empty nodes")
	}
}

func (dht *DHT) handleGetPeers(node *Node, response krpcMessage, foundQuery krpcQuery) {
	body := response.ResponseData
	infoHash, err := parseQueryHashValue("info_hash", foundQuery.QueryBody)
	if err != nil {
		log.Println(err)
		return
	}
	// Store token in tokenNodeMap for use later when announce_peers
	if body.Token == "" {
		log.Println("Error: missing token in handleGetPeers")
		return
	}
	dht.Rtable.tokenNodeMap[node.address.String()] = body.Token
	// If values present, return peers to client
	if len(body.Values) > 0 {
		log.Println("get_peers response with peers")
		// Parse and return peers to client through the FoundPeers channel
		for _, peerStr := range body.Values {
			peer, err := parsePeerStr(peerStr)
			if err != nil {
				log.Println(err)
				continue
			}
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

func (dht *DHT) respondPing(node *Node, query krpcMessage) {
	resBytes := makeResponse("r", query.TransactionID, responseBody{ID: string(dht.nodeID[:])})
	_, err := dht.socket.WriteToUDP(resBytes, &node.address)
	if err != nil {
		log.Println("Failed to respond ping with error: ", err)
	}
}

// respondFindNode returns the target node or K closest nodes to target
func (dht *DHT) respondFindNode(node *Node, query krpcMessage) {
	targetNodeID, err := parseQueryHashValue("target", query.QueryBody)
	if err != nil {
		log.Println(err)
		return
	}
	var nodesBuffer bytes.Buffer
	// Either return the target node if present, or its K closest nodes in rtable
	if n := dht.Rtable.getNodeFromID(targetNodeID); n != nil {
		nodesBuffer.WriteString(string(n.ID[:]) + n.address.String())
	} else {
		for _, n := range dht.Rtable.getClosestNodes(targetNodeID) {
			nodesBuffer.WriteString(string(n.ID[:]) + n.address.String())
		}
	}
	// Respond with nodes
	resBytes := makeResponse("r", query.TransactionID, responseBody{
		ID: string(dht.nodeID[:]),
		Nodes: nodesBuffer.String(),
	})
	_, err = dht.socket.WriteToUDP(resBytes, &node.address)
	if err != nil {
		log.Println("Failed to respond find_node with error: ", err)
	}
}

// TODO:
func (dht *DHT) respondGetPeers(node *Node, query krpcMessage) {
	// Return peers if present under info hash, else return K closest nodes
	infoHash, _ := parseQueryHashValue("info_hash", query.QueryBody)
	// Create token
	token := dht.createToken(node.address.String())
	var resBytes []byte
	if peers, ok := dht.infoHashPeerContacts[string(infoHash[:])]; ok {
		peersParsed := make([]string, len(peers))
		for _, p := range peers {
			peersParsed = append(peersParsed, p.Compact())
		}
		resBytes = makeResponse("r", query.TransactionID, responseBody{
			ID: string(dht.nodeID[:]),
			Values: peersParsed,
			Token: token,
		})
	} else {
		// Get K closest nodes to info hash
		var nodesBuffer bytes.Buffer
		for _, n := range dht.Rtable.getClosestNodes(infoHash) {
			nodesBuffer.WriteString(string(n.ID[:]) + n.address.String())
		}
		resBytes = makeResponse("r", query.TransactionID, responseBody{
			ID: string(dht.nodeID[:]),
			Nodes: nodesBuffer.String(),
			Token: token,
		})
	}
	// Respond with nodes
	_, err := dht.socket.WriteToUDP(resBytes, &node.address)
	if err != nil {
		log.Println("Failed to respond find_node with error: ", err)
	}
}

// respondAnnouncePeer handles announce_peer queries
// Update infoHashPeerContacts with node's IP and port specified in query body
func (dht *DHT) respondAnnouncePeer(node *Node, query krpcMessage) {
	// Verify token matches
	token, err := parseQueryKey("token", query.QueryBody)
	if err != nil {
		log.Println(err)
		return
	}
	if token != dht.createToken(node.address.String()) {
		log.Println("Error: token does not match node's token")
		return
	}
	// Save peer info under info hash in peer contacts
	infoHash, err := parseQueryKey("info_hash", query.QueryBody)
	port, err := parseQueryKeyInt("port", query.QueryBody)
	if infoHash == "" || port == -1 {
		log.Println(err)
		return
	}
	newPeer := Peer{node.address.IP, uint16(port)}
	dht.infoHashPeerContacts[infoHash] = append(dht.infoHashPeerContacts[infoHash], newPeer)
	// Respond with empty message
	resBytes := makeResponse("r", query.TransactionID, responseBody{ID: string(dht.nodeID[:])})
	_, err = dht.socket.WriteToUDP(resBytes, &node.address)
	if err != nil {
		log.Println("Failed to respond ping with error: ", err)
	}
}

////////////////////////////// Miscelaneous //////////////////////////////

// createToken returns a token seeded by key (node address)
func (dht *DHT) createToken(key string) string {
	h := sha1.New()
	io.WriteString(h, key)
	io.WriteString(h, dht.secret)
	return fmt.Sprintf("%x", h.Sum(nil))
}
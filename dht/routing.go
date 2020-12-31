package dht

import (
	"bytes"
	"net"
	"math/big"
	"sort"
	"log"
)

// RoutingTable stores buckets containing 'good' nodes
type RoutingTable struct {
	Buckets  	 []*bucket
	myNodeID 	 [20]byte			// copy of dht node id
	nodeMap  	 map[string]*Node	// maps address to node, key: UDPAddress string(ip:port)
	tokenNodeMap map[string]*Node	// maps token to node (for announce_peer)
	Size		 int				// number of nodes in rtable
}

// bucket contains nodes in the routing table
type bucket struct {
	minValue [20]byte // min ID value, inclusive
	maxValue [20]byte // max ID value, exclusive
	items    []*Node
}


// K max number of nodes per bucket
const K = 8
// MinTableSize is the minimum number of nodes in DHT before get_peers
var MinTableSize int = K*2

// NewRoutingTable creates a new routing table, initialized to single bucket
func NewRoutingTable(myNodeID [20]byte) *RoutingTable {
	newBucket := bucket{
		makeID(0, 1),
		makeID(160, 1),
		[]*Node{},
	}
	return &RoutingTable{
		[]*bucket{&newBucket}, 
		myNodeID,
		make(map[string]*Node),
		make(map[string]*Node),
		0,
	}
}

// getNode returns node or creates a new one and sets its id, also returns if node already exists
func (rtable *RoutingTable) getNodeOrCreate(addrStr string, id [20]byte) (*Node, bool, error) {
	addr, err := net.ResolveUDPAddr("udp", addrStr)
	if err != nil {
		return nil, false, err
	}
	if node, ok := rtable.nodeMap[addrStr]; ok {
		return node, true, nil
	}
	// Create a new node, insert into nodeMap
	node := NewNode(id, *addr)
	rtable.nodeMap[node.address.String()] = node
	return node, false, nil
}

// addNodeToDHT creates and inserts a node to rtable if not present, wraps getNodeOrCreate()
func (rtable *RoutingTable) addNodeToDHT(addr string, nodeID [20]byte) error {
	node, exists, err := rtable.getNodeOrCreate(addr, nodeID)
	if err != nil {
		return err
	}
	if exists  {
		log.Println("node ", addr, " exists")
	} else {
		return rtable.insertNode(node)
	}
	return nil
}


// insertNode inserts newNode (id) into rtable if possible
func (rtable *RoutingTable) insertNode(newNode *Node) error {
	// Insert into nodeMap
	rtable.nodeMap[newNode.address.String()] = newNode

	// Find bucket newNode falls into
	curBucket, index := rtable.findBucket(newNode.ID)
	// Insert node into curBucket if not full
	if len(curBucket.items) < K {
		curBucket.items = append(curBucket.items, newNode)
		rtable.Size++
	} else {
		// A bit more complicated if curBucket is full
		// 1. If thisNode ID falls in curBucket's ID space, split curBucket in 2
		// 2. If there are any bad/questionable nodes in curBucket, replace it with newNode (after pinging)
		// 3. Else, discard newNode since all nodes in curBucket are good, remove from nodeMap
		if bytes.Compare(rtable.myNodeID[:], curBucket.minValue[:]) >= 0 &&
			bytes.Compare(rtable.myNodeID[:], curBucket.maxValue[:]) < 0 {
			rtable.splitBucket(*curBucket, index)
			// Insert newNode again
			rtable.insertNode(newNode)
		} else {
			// For now, just replace the LRU node with newNode (instead of cases 2 and 3)
			nodeLRU, indexLRU := getLRUNodeInBucket(curBucket.items)
			curBucket.items[indexLRU] = newNode
			// delete nodeLRU from rtable
			delete(rtable.nodeMap, nodeLRU.address.String())
			delete(rtable.tokenNodeMap, nodeLRU.address.String())
		}
	}
	return nil
}

// getClosestNodes returns the K closest nodes to hashID
// returned nodes are sorted in increasing distance from hashID, so first is closest
func (rtable *RoutingTable) getClosestNodes(hashID [20]byte) []*Node {
	num := K
	if rtable.Size < 8 {
		log.Println("Warning getClosestNodes: rtable.Size < 8")
		num = rtable.Size
	}
	closestNodes := []*Node{}
	foundBucket, index := rtable.findBucket(hashID)
	log.Println("num buckets:", len(rtable.Buckets),"foundBucket index:", index)
	// Add all nodes in foundBucket except the node with ID matching hashID if present
	for _, n := range foundBucket.items {
		if n.ID != hashID {
			closestNodes = append(closestNodes, n)
		}
	}
	// Keep iterating lower and upper buckets until we have K closest nodes
	increment := 1
	for len(closestNodes) < num && increment < len(rtable.Buckets) {
		log.Println(closestNodes)
		if index - increment >= 0 {
			closestNodes = append(closestNodes, rtable.Buckets[index-increment].items...)
		}
		if index + increment < len(rtable.Buckets) {
			closestNodes = append(closestNodes, rtable.Buckets[index+increment].items...)
		}
		increment++
	}
	// Sort closestNodes before returning, at most sorts 3*K nodes, so constant time  complexity 
	closestNodes = rtable.xorSortedNodes(hashID, closestNodes)[:num]
	return closestNodes
}

// xorSortedNodes sorts (increasing order) list of nodes based on XOR distance from hashID
func (rtable *RoutingTable) xorSortedNodes(hashID [20]byte, nodes []*Node) []*Node {
	// Sort nodes based on node.ID XOR hashID
	bigNodeID := new(big.Int).SetBytes(hashID[:])
	sort.Slice(nodes, func (i, j int) bool {
		bigI := new(big.Int).SetBytes(nodes[i].ID[:])
		bigJ := new(big.Int).SetBytes(nodes[j].ID[:])
		xorI, xorJ := bigI.Xor(bigI, bigNodeID), bigJ.Xor(bigJ, bigNodeID)
		return xorI.Cmp(xorJ) <= 0
	})
	return nodes
}

// findBucket finds the bucket a node id falls under and its index
// Time complexity: O(log N) where N is number of buckets
func (rtable *RoutingTable) findBucket(nodeID [20]byte) (*bucket, int) {
	bucketList := rtable.Buckets
	nodeIDStr := string(nodeID[:])
	// do binary search to find appropriate bucket
	left := 0
	right := len(bucketList) - 1
	var mid int
	var cur *bucket
	for left <= right {
		mid = left + (right-left)/2
		cur = bucketList[mid]
		if nodeIDStr < string(cur.minValue[:]) {
			right = mid - 1
		} else if nodeIDStr >= string(cur.maxValue[:]) {
			left = mid + 1
		} else {
			return cur, mid
		}
	}
	// Bucket not found, just return cur, mid
	// I think this case will not run since rtable.Buckets covers entire [0,2^160] space
	return cur, mid
}

// splitBucket splits curBucket into 2 new buckets and updates rtable, returning successful flag
func (rtable *RoutingTable) splitBucket(curBucket bucket, index int) bool {
	var lowerBucket, upperBucket bucket
	var medValue [20]byte
	// New table with only one bucket is split differently
	if len(rtable.Buckets) == 1 {
		lowerBucket = bucket{makeID(0, 1), makeID(159, 0), []*Node{}}
		upperBucket = bucket{makeID(159, 0), makeID(160, 1), []*Node{}}
	} else {
		// Split bucket space equally in half
		medValue = getMiddle(curBucket.maxValue, curBucket.minValue)
		lowerBucket = bucket{curBucket.minValue, medValue, []*Node{}}
		upperBucket = bucket{medValue, curBucket.maxValue, []*Node{}}
	}
	// Update items in curBucket and newBucket
	for _, node := range curBucket.items {
		if bytes.Compare(node.ID[:], medValue[:]) >= 0 {
			upperBucket.items = append(upperBucket.items, node)
		} else {
			lowerBucket.items = append(lowerBucket.items, node)
		}
	}
	// Update rtable.Buckets with split buckets
	buckets := rtable.Buckets
	// Delete curBucket at index
	buckets = append(buckets[:index], buckets[index+1:]...)	
	// Insert lower and upper buckets at index
	buckets = append(buckets, &bucket{})
	copy(buckets[index+1:], buckets[index:])
	buckets[index] = &lowerBucket
	buckets = append(buckets, &bucket{})
	copy(buckets[index+2:], buckets[index+1:])
	buckets[index+1] = &upperBucket

	// Update rtable.Buckets
	rtable.Buckets = buckets
	return true
}

// getNodeFromID finds and returns node from its ID if present
func (rtable *RoutingTable) getNodeFromID(nodeID [20]byte) *Node {
	b, _ := rtable.findBucket(nodeID)
	for _, n := range b.items {
		if n.ID == nodeID {
			return n
		}
	}
	return nil
}

// makeID returns the node ID (160-bits) representation of 2^pow - rem
// e.g. makeID(160, 1) is the largest possible node ID
func makeID(pow, rem int) [20]byte {
	if pow < 0 || pow > 160 || rem < 0 || pow == 160 && rem <= 0 {
		panic("Error - node id out of bounds (160-bit)")
	}
	var b [20]byte
	test := new(big.Int)
	test = test.Exp(big.NewInt(2), big.NewInt(int64(pow)), nil)
	test = test.Sub(test, big.NewInt(int64(rem)))
	test.FillBytes(b[:])
	return b
}

// getMiddle returns the middle node id value between low and high
func getMiddle(low, high [20]byte) [20]byte {
	// Use math/big package to do arithmetic
	lowBig := new(big.Int).SetBytes(low[:])
	highBig := new(big.Int).SetBytes(high[:])
	middle := new(big.Int)
	middle = middle.Div(big.NewInt(0).Sub(highBig, lowBig), big.NewInt(2))
	middle = middle.Add(lowBig, middle)
	// Convert back to [20]byte
	middleBytes := [20]byte{}
	middle.FillBytes(middleBytes[:])
	return middleBytes
}

// getLRUNodeInBucket returns node with least recent lastContact, nodes must not be empty 
func getLRUNodeInBucket(nodes []*Node) (*Node, int) {
	nodeLRU := nodes[0]
	indexLRU := 0
	for i, node := range nodes {
		if nodeLRU.lastContact.Before(node.lastContact) {
			nodeLRU = node
			indexLRU = i
		}
	}
	return nodeLRU, indexLRU
}

func (rtable *RoutingTable) printBuckets() {
	log.Println("Buckets:")
	for _, b := range rtable.Buckets {
		log.Println("lower:",b.minValue)
		log.Println("upper:",b.maxValue)
		log.Println("items:",b.items)
		log.Println("-------------------------")
	}
}
package dht

import (
	"bytes"
	"net"
	"math/big"
)

// RoutingTable stores buckets containing 'good' nodes
type RoutingTable struct {
	Buckets  	 []*bucket
	myNodeID 	 [20]byte			// copy of dht node id
	nodeMap  	 map[string]*Node	// maps address to node, key: UDPAddress string(ip:port)
	tokenNodeMap map[string]string	// maps node address to token (for announce_peer)
}

// bucket contains nodes in the routing table
type bucket struct {
	minValue [20]byte // min ID value, inclusive
	maxValue [20]byte // max ID value, exclusive
	items    []*Node
}


// K max number of nodes per bucket
const K = 8

// NewRoutingTable creates a new routing table, initialized to single bucket
func NewRoutingTable(myNodeID [20]byte) *RoutingTable {
	newBucket := bucket{
		[20]byte{},
		makeID(159, 255),
		[]*Node{},
	}
	return &RoutingTable{
		[]*bucket{&newBucket}, 
		myNodeID,
		make(map[string]*Node),
		make(map[string]string),
	}
}

// getNode returns node or creates a new one and sets its id
func (rtable *RoutingTable) getNodeOrCreate(addrStr string, id [20]byte) (*Node, error) {
	addr, err := net.ResolveUDPAddr("udp", addrStr)
	if err != nil {
		return nil, err
	}
	if node, ok := rtable.nodeMap[addrStr]; ok {
		return node, nil
	}
	// Create a new node without an ID for now
	node := NewNode(id, *addr)
	err = rtable.insertNode(node)
	return node, err
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
	} else {
		// TODO: A bit more complicated if curBucket is full
		// 1. If thisNode ID falls in curBucket's ID space, split curBucket in 2
		// 2. If there are any bad/questionable nodes in curBucket, replace it with newNode (after pinging)
		// 3. Else, discard newNode since all nodes in curBucket are good, remove from nodeMap
		if bytes.Compare(rtable.myNodeID[:], curBucket.minValue[:]) >= 0 &&
			bytes.Compare(rtable.myNodeID[:], curBucket.maxValue[:]) < 0 {
			rtable.splitBucket(*curBucket, index)
			// Insert newNode
			rtable.insertNode(newNode)
		} else {
			// Check for questionable/bad nodes and ping them

		}
	}
	return nil
}

// findBucket finds the bucket a node id falls under and its index
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
		lowerBucket = bucket{makeID(159, 0), makeID(160, 1), []*Node{}}
		upperBucket = bucket{makeID(0, 0), makeID(159, 0), []*Node{}}
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
	// Update rtable.Buckets at index
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
	return true
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


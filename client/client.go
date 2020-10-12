package client

import (
	"crypto/sha1"
	// "io/ioutil"
	"github.com/dave2000sang/torrent-go/torrent"
	"github.com/jackpal/bencode-go"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
	"io"
	"encoding/binary"
	// "fmt"
	// "bytes"
)

// Client represents running BitTorrent client instance
type Client struct {
	TorrentFile torrent.Torrent
	ID          [20]byte
	PeerList    []Peer
}

// Peer represents a seeder
// TODO - Move to own package?
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

type bencodeResponse struct {
	FailureReason string `bencode:"failure reason"`
	Interval      int    `bencode:"warning message"`
	TrackerID     string `bencode:"interval"`
	Complete      int    `bencode:"complete"`
	Incomplete    int    `bencode:"incomplete"`
	PeerList      string `bencode:"peers"`
}

// NewClient creates a new Client object
func NewClient() *Client {
	uniqueHash := time.Now().String() + strconv.Itoa(rand.Int())
	c := Client{ID: sha1.Sum([]byte(uniqueHash))}
	return &c
}

// ConnectTracker makes get request tracker, returning response object
func (client *Client) ConnectTracker() {
	u, err := url.Parse(client.TorrentFile.Announce)
	q, err := url.ParseQuery(u.RawQuery)

	// Add query params
	q.Add("peer_id", string(client.ID[:]))
	q.Add("info_hash", string(client.TorrentFile.InfoHash[:]))
	q.Add("port", "6881")
	q.Add("uploaded", "0")
	q.Add("downloaded", "0")
	q.Add("left", strconv.Itoa(client.TorrentFile.FileLength))
	q.Add("compact", "1")
	q.Add("event", "started")

	// url.String() returns escaped url
	u.RawQuery = q.Encode()
	response, err := http.Get(u.String())
	if err != nil {
		// TODO try request again with updated params (compact=0, try a different tracker url, etc.)
		log.Fatal(err)
	}
	defer response.Body.Close()

	// Handle tracker response
	benRes := bencodeResponse{}
	err = bencode.Unmarshal(response.Body, &benRes)
	if err != nil {
		log.Fatal(err)
	}
	if benRes.FailureReason != "" {
		log.Fatal(benRes.FailureReason)
	}
	// Parse peers into client
	peers := benRes.PeerList
	for i := 0; i < len(peers)-6; i += 6 {
		ip := net.IP([]byte(peers[i : i+4]))
		port := binary.BigEndian.Uint16([]byte(peers[i+4 : i+6]))
		newPeer := Peer{ip, port, status{true, false, true, false}}
		client.PeerList = append(client.PeerList, newPeer)
	}
}

// ConnectPeers connects to each peer
func (client *Client) ConnectPeers() {
	for _, peer := range client.PeerList {
		client.doHandshake(peer)
	}
}

// Initialize handshake with peer
func (client *Client) doHandshake(peer Peer) {
	handshake := []byte{}
	handshake = append(handshake, []byte{byte(19)}...)
	handshake = append(handshake, []byte("BitTorrent protocol")...)
	handshake = append(handshake, []byte{0, 0, 0, 0, 0, 0, 0, 0}...)
	handshake = append(handshake, client.TorrentFile.InfoHash[:]...)
	handshake = append(handshake, client.ID[:]...)

	// // Verify handshake
	// err := ioutil.WriteFile("tmp", handshake, 0644)
	// infoHash := client.TorrentFile.InfoHash	// [20]byte	
	// fmt.Printf("infohash = %x\n", infoHash)
	// fmt.Println("len = ", len(infoHash))

	// Create TCP connection with peer
	peerAddr := peer.IP.String() + ":" + strconv.Itoa(int(peer.Port))
	log.Println("Performing handshake with peer: ", peer.IP.String())
	tcpAddr, err := net.ResolveTCPAddr("tcp", peerAddr)
	if err != nil {
		log.Fatal("ResolveTCPAddr failed on ", peerAddr)
	}
	conn, err := net.DialTCP("tcp", nil, tcpAddr)
	if err != nil {
		log.Fatal("Failed to DialTCP on ", peerAddr)
	}
	defer conn.Close()

	_, err = conn.Write(handshake)
	if err != nil {
		conn.Close()
		log.Fatal("Error writing handshake: ", err)
	}

	// read peer response
	// response := make([]byte, 1024)
	response := []byte{}
	for {
		n, err := conn.Read(response)
		if err != nil {
			if err == io.EOF {
				// Handle closed connection
				log.Fatal("Peer closed connection")
			}
			log.Fatal("Error reading peer response: ", err)
		}
		// Handle 'Keep-alive' message
		if n == 0 {
			log.Println("handling keep-alive...")
			log.Println(response)
			time.Sleep(180 * time.Second)
		} else {
			break
		}
	}

	log.Println("Handshake response: ", string(response))	
}

func checkError(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
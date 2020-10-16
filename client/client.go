package client

import (
	"crypto/sha1"
	// "io/ioutil"
	"encoding/binary"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/dave2000sang/torrent-go/peer"
	"github.com/dave2000sang/torrent-go/torrent"
	"github.com/jackpal/bencode-go"
	// "fmt"
)

// Client represents running BitTorrent client instance
type Client struct {
	TorrentFile torrent.Torrent
	ID          [20]byte
	PeerList    []*peer.Peer
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
		newPeer, _ := peer.NewPeer(ip, port)
		client.PeerList = append(client.PeerList, newPeer)
	}
}

// ConnectPeers connects to each peer
// TODO - connect to each peer concurrently
func (client *Client) ConnectPeers() {
	for _, peer := range client.PeerList {
		peer.DoHandshake(client.TorrentFile.InfoHash, client.ID)
		log.Println("--------------------------")
	}
}

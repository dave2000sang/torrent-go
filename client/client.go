package client

import (
	"crypto/sha1"
	"errors"

	// "io/ioutil"
	"encoding/binary"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
	"torrent-go/peer"
	"torrent-go/piece"
	"torrent-go/torrent"
	"torrent-go/utils"

	"github.com/jackpal/bencode-go"
	// "fmt"
)

// Client represents BitTorrent client instance
type Client struct {
	TorrentFile torrent.Torrent
	ID          [20]byte
	PeerList    []*peer.Peer
	Pieces      []*piece.Piece
}

type bencodeTrackerResponse struct {
	FailureReason string `bencode:"failure reason"`
	Interval      int    `bencode:"warning message"`
	TrackerID     string `bencode:"interval"`
	Complete      int    `bencode:"complete"`
	Incomplete    int    `bencode:"incomplete"`
	PeerList      string `bencode:"peers"`
}

// Blocksize for downloading pieces
const Blocksize = 16384 // Block size = 16KB

// NewClient creates a new Client object
func NewClient(curTorrent torrent.Torrent) *Client {
	uniqueHash := time.Now().String() + strconv.Itoa(rand.Int())
	piecesList := []*piece.Piece{}
	for i := 0; i < curTorrent.NumPieces; i++ {
		piecesList = append(piecesList, piece.NewPiece(i))
	}
	c := Client{
		TorrentFile: curTorrent,
		ID:          sha1.Sum([]byte(uniqueHash)),
		Pieces:      piecesList,
	}
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
	benRes := bencodeTrackerResponse{}
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
		// Initialize peer.HavePieces to size <num pieces>/8
		newPeer.HavePieces = make([]byte, utils.DivisionCeil(client.TorrentFile.NumPieces, 8))
		client.PeerList = append(client.PeerList, newPeer)
	}
}

// ConnectPeers connects to each peer, initiates handshake
// TODO - connect to each peer concurrently
func (client *Client) ConnectPeers() {
	for _, peer := range client.PeerList {
		err := peer.DoHandshake(client.TorrentFile.InfoHash, client.ID)
		if err != nil {
			log.Println(err)
		}
		log.Println("--------------------------")
	}
}

// DownloadPieces downloads pieces from peers
func (client *Client) DownloadPieces() {
	for _, peer := range client.PeerList {
		if !peer.Status.PeerChocking && peer.Status.AmInterested { // ready to receive requests for pieces
			log.Println("Begin downloading from peer ", peer.IP.String())
			err := client.startDownload(peer)
			if err != nil {
				log.Println(err)
				continue
			}

			// TODO - check if all pieces finished downloading
			log.Println("==========================")			
		}
	}
}

// startDownload begins downloading pieces from peer
func (client *Client) startDownload(peer *peer.Peer) error {
	// Form TCP connection with peer
	conn, err := peer.TCPConnect()
	if err != nil {
		return err
	}
	defer conn.Close()

	// Keep requesting blocks until piece is complete
	totalPieceSize := client.TorrentFile.PieceLength
	pieceIndex, blockOffset, curPiece := peer.GetNextPiece(client.Pieces)
	if pieceIndex == -1 {
		// Either all pieces finished, or this peer does not have any pieces we need
		return nil
	}
	log.Println("Next piece index: ", pieceIndex)
	curBlockSize := Blocksize
	for blockOffset < totalPieceSize {
		log.Println("blockOffset: ", blockOffset)
		requestMsg := make([]byte, 17)
		binary.BigEndian.PutUint32(requestMsg[:4], 19)

		copy(requestMsg[4:5], []byte{byte(1)})
		binary.BigEndian.PutUint32(requestMsg[5:9], uint32(pieceIndex))
		binary.BigEndian.PutUint32(requestMsg[9:13], uint32(blockOffset))
		binary.BigEndian.PutUint32(requestMsg[13:17], uint32(curBlockSize))
		// Send REQUEST piece message
		_, err = conn.Write(requestMsg)
		if err != nil {
			return err
		}
		log.Println("Sent request for block")
		// Parse peer response
		msgID, msgPayload, err := peer.ReadMessage(conn)
		if err != nil {
			return err
		}
		if msgID != 9 {
			return errors.New("Peer did not respond with piece message")
		}
		log.Println("Updating piece with block")
		curPiece.UpdatePieceWithBlock(msgPayload, requestMsg[5:])
		// keep looping until piece is completely downloaded
		blockOffset += curBlockSize
		log.Println("~~~~~~~~~~~~~~~~~~~~~~~")
	}
	curPiece.IsComplete = true
	curPiece.IsDownloading = false
	return nil
}



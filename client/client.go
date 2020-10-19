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
			continue
		}
		log.Println("--------------------------")
	}
}

// DownloadPieces downloads pieces from peers
func (client *Client) DownloadPieces() {
	for _, peer := range client.PeerList {
		if !peer.Status.PeerChocking && peer.Status.AmInterested {
			// peer is ready to receive requests for pieces
			err := client.startDownload(peer)
			if err != nil {
				log.Println(err)
				continue
			}

			// TODO - check if all pieces finished downloading
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
	totalPieceSize = client.TorrentFile.PieceLength
	pieceIndex, blockOffset, curPiece := client.getNextPiece()
	curBlockSize = BlockSize
	for blockOffset < totalPieceSize {
		requestMsg := make([]byte, 17)
		binary.BigEndian.PutUint32(requestMsg[:4], 19)
		if pieceIndex == -1 {
			// Either all pieces finished, or this peer does not have any pieces we need
			return nil
		}
		copy(requestMsg[4:5], []byte{byte(1)})
		binary.BigEndian.PutUint32(requestMsg[5:9], uint32(pieceIndex))
		binary.BigEndian.PutUint32(requestMsg[9:13], uint32(blockOffset))
		binary.BigEndian.PutUint32(requestMsg[13:17], uint32(curBlockSize))
		// Send peer a REQUEST piece message
		_, err = conn.Write(requestMsg)
		if err != nil {
			return err
		}
		// Parse peer response
		msgID, msgPayload, err := peer.ReadMessage(conn)
		if err != nil {
			return err
		}
		if msgID != 9 {
			return errors.New("Peer did not respond with piece message")
		}
		updatePieceWithBlock(msgPayload, requestMsg[5:], curPiece)
		// keep looping until piece is completely downloaded
		blockOffset += curBlockSize
	}
	return nil
}

// getNextPiece finds the next incomplete piece that peer owns
func (client *Client) getNextPiece(peer *peer.Peer) (int, int, *piece.Piece) {
	for _, piece := range client.Pieces {
		if !piece.IsComplete {
			// Check that peer has the piece
			pieceIndex := piece.Index
			if peer.HasPiece(pieceIndex) {
				return pieceIndex, piece.BlockIndex, piece
			}
		}
	}
	return -1, 0, nil
}


// updatePieceWithBlock updates client piece
func updatePieceWithBlock(payload []byte, requestMsg []byte, piece *piece.Piece) error {
	requestPieceBody := requestMsg[5:]
	// pieceIndex := payload[:4]
	// pieceBegin := payload[4:8]
	pieceBlock := payload[8:]
	// Check if piece index and begin match requested
	if !bytes.Equal(payload[:8], requestPieceBody[:8]) ||
		len(pieceBlock) != int(binary.BigEndian.Uint32(requestMsg[13:17])) {
		return errors.New("ERROR: Peer sent piece doesn't match requested piece")
	}
	// Save block to Piece struct
	piece.Blocks = append(piece.Blocks, pieceBlock...)
	piece.BlockIndex += len(pieceBlock)
	piece.IsDownloading = true
	return nil
}
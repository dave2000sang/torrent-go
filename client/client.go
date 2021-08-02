package client

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"
	"torrent-go/peer"
	"torrent-go/piece"
	"torrent-go/torrent"

	"github.com/jackpal/bencode-go"
)

// Client represents BitTorrent client instance
type Client struct {
	TorrentFile  torrent.Torrent
	ID           [20]byte
	PeerList     []*peer.Peer
	Pieces       []*piece.Piece
	DownloadFile *os.File // opened file to write pieces to
	DownloadPath string
}

type bencodeTrackerResponse struct {
	FailureReason string `bencode:"failure reason"`
	Interval      int    `bencode:"warning message"`
	TrackerID     string `bencode:"interval"`
	Complete      int    `bencode:"complete"`
	Incomplete    int    `bencode:"incomplete"`
	PeerList      string `bencode:"peers"`
}

// NewClient creates a new Client object
func NewClient(curTorrent torrent.Torrent) (*Client, error) {
	uniqueHash := time.Now().String() + strconv.Itoa(rand.Int())
	piecesList := []*piece.Piece{}
	for i := 0; i < curTorrent.NumPieces; i++ {
		piecesList = append(piecesList, piece.NewPiece(i, curTorrent.PieceHashList[i]))
	}
	// Create or open file to write pieces to
	filePath := curTorrent.FileName
	var oldContent = make([]byte, curTorrent.FileLength)
	// if file exists, update pieces list to avoid re-downloading existing pieces
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		log.Println("Using existing file at ", filePath)
		oldContent, err = ioutil.ReadFile(filePath)
		if err != nil {
			return nil, err
		}
		if len(oldContent) != curTorrent.FileLength {
			log.Println(len(oldContent), " != ", curTorrent.FileLength)
			return nil, fmt.Errorf("existing file %s does not match length specified in torrent file", filePath)
		}
		// Update client pieces info with existing pieces
		pieceLength := curTorrent.PieceLength
		emptyPiece := make([]byte, pieceLength)
		// havePieces := []int{}
		for i := 0; i < curTorrent.NumPieces-1; i++ {
			if !bytes.Equal(oldContent[i*pieceLength : (i+1)*pieceLength], emptyPiece) {
				// havePieces = append(havePieces, i)
				log.Println("already have piece: ", i);
				piecesList[i].IsComplete = true
			} else {
				// log.Println("Piece [", i, "] missing")
			}
		}
		// log.Println("Previously downloaded pieces: ", havePieces)
	}
	file, err := os.Create(filePath)
	if err != nil {
		return nil, err
	}
	file.Write(oldContent)

	c := Client{
		TorrentFile:  curTorrent,
		ID:           sha1.Sum([]byte(uniqueHash)),
		Pieces:       piecesList,
		DownloadFile: file,
		DownloadPath: filePath,
	}
	return &c, nil
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
		newPeer, _ := peer.NewPeer(ip, port, client.TorrentFile.NumPieces)
		// Initialize peer.HavePieces to size <num pieces>/8
		client.PeerList = append(client.PeerList, newPeer)
	}
}

// ConnectPeers connects to each peer, initiates handshake
// Uses 2 piece queues, one for sending pieces to download, one for receiving downloaded pieces
func (client *Client) ConnectPeers(UseDHT bool) {
	pieceQueue := make(chan *piece.Piece)
	results := make(chan *piece.Piece)

	// Add pieces to pieceQueue channel for downloading
	go func() {
		for _, piece := range client.Pieces {
			if !piece.IsComplete && !piece.IsDownloading {
				pieceQueue <- piece
			}
		}
	}()

	// Trigger main worker to handshake and download pieces concurrently
	for _, peer := range client.PeerList {
		go client.mainWorker(peer, pieceQueue, results, UseDHT)
	}
	
	// listen for finished download pieces
	for !finishedDownloading(client.Pieces) {
		finishedPiece := <- results
		err := client.verifyPieceAndWriteDisk(finishedPiece)
		if err != nil {
			log.Println(err)
			log.Println("Failed to verify/write piece: ", finishedPiece.Index)
			// Reset piece and add back to pieceQueue
			finishedPiece.Reset()
			pieceQueue <- finishedPiece
		}
	}
	// finished downloading all pieces
	log.Println("DOWNLOAD COMPLETE, file path: ", client.DownloadPath)
}

// mainWorker is run concurrently with ConnectPeers()
func (client *Client) mainWorker(peer *peer.Peer, pieceQueue, results chan *piece.Piece, UseDHT bool) {
	log.Println("Running mainWorker on peer", peer.IP)
	// handshake with peer
	err := peer.DoHandshake(client.TorrentFile.InfoHash, client.ID, UseDHT)
	if err != nil {
		log.Println(err)
		return
	}
	if peer.Connection != nil {
		defer peer.Connection.Close()
	}
	// Keep requesting and downloading pieces from pieceQueue
	for {
		if !peer.Status.PeerChocking && peer.Status.AmInterested {
			curPiece := <- pieceQueue
			// log.Printf("Begin downloading piece [%d] from peer %s \n", curPiece.Index, peer.IP.String())
			err = client.startDownload(peer, curPiece)
			if err != nil {
				log.Println(err)
				pieceQueue <- curPiece
				return
			}
			// Write downloaded piece back
			results <- curPiece
			// TODO - check if all pieces finished downloading
		} else {
			return
		}
	}
}

func (client *Client) verifyPieceAndWriteDisk(piece *piece.Piece) error {
	// Verify downloaded piece against SHA1 hash
	if piece.Verify() {
		err := piece.WriteToDisk(client.TorrentFile.FileName, client.DownloadFile, client.TorrentFile.PieceLength)
		if err != nil {
			return err
		}
		return nil
	}
	return errors.New("piece does not match SHA1 hash")
}

// startDownload begins downloading piece from peer
func (client *Client) startDownload(peer *peer.Peer, curPiece *piece.Piece) error {
	conn := peer.Connection
	if conn == nil {
		return errors.New("TCP Connection is nil")
	}

	// Either all pieces finished, or this peer does not have any pieces we need
	if curPiece == nil {
		return errors.New("ERROR: piece is nil")
	}

	// Keep requesting blocks until piece is complete
	totalPieceSize := client.TorrentFile.PieceLength
	pieceIndex := curPiece.Index
	blockOffset := curPiece.BlockIndex
	curBlockSize := piece.Blocksize
	// log.Printf("starting downloading piece [%d]\n", pieceIndex)

	for blockOffset < totalPieceSize {
		requestMsg := make([]byte, 17)
		binary.BigEndian.PutUint32(requestMsg[:4], 13)

		// Create request message (id: 6)
		copy(requestMsg[4:5], []byte{byte(6)})
		binary.BigEndian.PutUint32(requestMsg[5:9], uint32(pieceIndex))
		binary.BigEndian.PutUint32(requestMsg[9:13], uint32(blockOffset))
		binary.BigEndian.PutUint32(requestMsg[13:17], uint32(curBlockSize))
		
		// Send REQUEST piece message
		_, err := conn.Write(requestMsg)
		if err != nil {
			return err
		}
		// Parse peer response
		msgID, msgPayload, err := peer.ReadMessage(conn)
		// write payload for debugging
		// utils.WriteFile("payloadDump", msgPayload)

		if err != nil {
			return err
		}
		if msgID != 7 {
			log.Println("startDownload: peer sent message id: ", msgID)
			return errors.New("peer did not respond with piece message")
		}

		curPiece.UpdatePieceWithBlock(msgPayload, requestMsg[5:])
		// keep looping until piece is completely downloaded
		blockOffset += curBlockSize
	}
	curPiece.IsComplete = true
	curPiece.IsDownloading = false
	log.Println("Finished downloading piece: ", pieceIndex)
	return nil
}

// AddNewPeer adds a new peer to peer list, handles duplicates
func (client *Client) AddNewPeer(newPeer *peer.Peer) {
	for _, p := range client.PeerList {
		if p.IP.String() == newPeer.IP.String() {
			log.Println("Got duplicate peer:", newPeer)
			return
		}
	}
	client.PeerList = append(client.PeerList, newPeer)	
}

// TODO - optimize this, currently runs in O(n) where n = num pieces
func finishedDownloading(pieces []*piece.Piece) bool {
	for _, p := range pieces {
		if !p.IsComplete {
			return false
		}
	}
	return true
}
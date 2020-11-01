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
	"torrent-go/utils"

	"github.com/jackpal/bencode-go"
)

// Client represents BitTorrent client instance
type Client struct {
	TorrentFile  torrent.Torrent
	ID           [20]byte
	PeerList     []*peer.Peer
	Pieces       []*piece.Piece
	DownloadFile *os.File // opened file to write pieces to
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
	log.Println("path = ", filePath)
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		log.Println("File already exists")
		oldContent, err = ioutil.ReadFile(filePath)
		if err != nil {
			return nil, err
		}
		if len(oldContent) != curTorrent.FileLength {
			log.Println(len(oldContent), " != ", curTorrent.FileLength)
			return nil, fmt.Errorf("Error: existing file %s does not match length specified in torrent file", filePath)
		}
		// Update client pieces info with existing pieces
		pieceLength := curTorrent.PieceLength
		emptyPiece := make([]byte, pieceLength)
		for i := 0; i < curTorrent.NumPieces-1; i++ {
			var existingPiece []byte
			if i == curTorrent.NumPieces-1 {
				existingPiece = oldContent[i*pieceLength:]
			}
			existingPiece = oldContent[i*pieceLength : i*pieceLength+pieceLength]
			if !bytes.Equal(existingPiece, emptyPiece) {
				log.Println("Using existing piece", i)
				piecesList[i].IsComplete = true
			}
		}

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
		newPeer, _ := peer.NewPeer(ip, port)
		// Initialize peer.HavePieces to size <num pieces>/8
		newPeer.HavePieces = make([]byte, utils.DivisionCeil(client.TorrentFile.NumPieces, 8))
		client.PeerList = append(client.PeerList, newPeer)
	}
}

// ConnectPeers connects to each peer, initiates handshake
// TODO - connect to each peer concurrently
func (client *Client) ConnectPeers() {
	// For now, only connect to first 3 peers for testing
	for _, peer := range client.PeerList[:1] {
		conn, err := peer.DoHandshake(client.TorrentFile.InfoHash, client.ID)
		// Persist tcp connection
		peer.Connection = conn
		if err != nil {
			if conn != nil {
				conn.Close()
			}
			log.Println(err)
		}
		log.Println("--------------------------")
	}
}

// DownloadPieces downloads pieces from peers
func (client *Client) DownloadPieces() {
	for _, peer := range client.PeerList {
		// If peer is ready to receive requests for pieces
		if !peer.Status.PeerChocking && peer.Status.AmInterested {
			log.Println("Begin downloading from peer ", peer.IP.String())
			piece, err := client.startDownload(peer)
			if err != nil {
				log.Println(err)
				continue
			}
			// Verify downloaded piece against SHA1 hash
			if piece.Verify() {
				err = piece.WriteToDisk(client.TorrentFile.FileName, client.DownloadFile, client.TorrentFile.PieceLength)
				if err != nil {
					log.Println(err)
				}
				log.Printf("Wrote piece [%d] to file\n", piece.Index)
			} else {
				log.Println("Error: piece does not match SHA1 hash, skipping")
			}

			// TODO - check if all pieces finished downloading
			log.Println("==========================")
		}
	}
}

// startDownload begins downloading pieces from peer
func (client *Client) startDownload(peer *peer.Peer) (*piece.Piece, error) {
	conn := peer.Connection
	if conn == nil {
		return nil, errors.New("TCP Connection is nil")
	}
	defer conn.Close()
	// // Form TCP connection with peer
	// conn, err := peer.TCPConnect()
	// if err != nil {
	// 	return err
	// }

	// Keep requesting blocks until piece is complete
	totalPieceSize := client.TorrentFile.PieceLength
	pieceIndex, blockOffset, curPiece := peer.GetNextPiece(client.Pieces)
	if pieceIndex == -1 {
		// Either all pieces finished, or this peer does not have any pieces we need
		return nil, nil
	}
	log.Println("Next piece index: ", pieceIndex)
	curBlockSize := piece.Blocksize
	for blockOffset < totalPieceSize {
		log.Println("blockOffset: ", blockOffset)
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
			return nil, err
		}
		log.Println("Sent request for block")
		// Parse peer response
		msgID, msgPayload, err := peer.ReadMessage(conn)
		// write payload for debugging
		// utils.WriteFile("payloadDump", msgPayload)

		if err != nil {
			return nil, err
		}
		if msgID != 7 {
			return nil, errors.New("Peer did not respond with piece message")
		}

		curPiece.UpdatePieceWithBlock(msgPayload, requestMsg[5:])
		log.Println("Updated piece with block")
		// keep looping until piece is completely downloaded
		blockOffset += curBlockSize
		log.Println("~~~~~~~~~~~~~~~~~~~~~~~")
	}
	curPiece.IsComplete = true
	curPiece.IsDownloading = false
	log.Println("Finished downloading piece: ", pieceIndex)
	return curPiece, nil
}

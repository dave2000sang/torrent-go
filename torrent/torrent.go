package torrent

import (
	"bytes"
	"crypto/sha1"
	"errors"
	"log"
	"os"
	"torrent-go/utils"

	"github.com/jackpal/bencode-go"
)

// Torrent is a .torrent file
type Torrent struct {
	Announce      string
	InfoHash      [20]byte
	FileName      string
	FileLength    int
	NumPieces     int
	PieceLength   int
	PieceHashList [][]byte // PieceHashList[i] is the 20 length SHA1 hash of the ith piece
}

// bencodeInfo for bencode-go package
type bencodeInfo struct {
	Pieces      string `bencode:"pieces"`
	PieceLength int    `bencode:"piece length"`
	Length      int    `bencode:"length"`
	Name        string `bencode:"name"`
	//Files       dict   `bencode:"files"` // For multi file case
}

// bencodeOytput for bencode-go package
type bencodeOutput struct {
	Announce string      `bencode:"announce"`
	Info     bencodeInfo `bencode:"info"`
}

// ReadTorrentFile reads a torrent file path
func ReadTorrentFile(filePath string) (Torrent, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return Torrent{}, err
	}
	defer file.Close()

	tmp := bencodeOutput{}
	err = bencode.Unmarshal(file, &tmp)
	if err != nil {
		return Torrent{}, err
	}
	log.Println(tmp.Info.Name)
	return createTorrent(tmp)
}

func createTorrent(output bencodeOutput) (Torrent, error) {
	var infoHashes string = output.Info.Pieces
	// assert that infoHashes is multiple of 20
	if len(infoHashes)%20 != 0 {
		return Torrent{}, errors.New("torrent format error: info hash must be a multiple of 20 bytes")
	}
	var pieces [][]byte
	for i := 0; i <= len(infoHashes)-20; i += 20 {
		curPiece := []byte(infoHashes[i : i+20])
		pieces = append(pieces, curPiece)
	}
	// generate 20 byte SHA1 hash, to be sent to tracker server
	var buf bytes.Buffer
	bencode.Marshal(&buf, output.Info)
	// log.Printf("%x\n", sha1.Sum([]byte(buf.String())))
	newTorrent := Torrent{
		InfoHash:      sha1.Sum([]byte(buf.String())),
		Announce:      output.Announce,
		FileName:      output.Info.Name,
		FileLength:    output.Info.Length,
		NumPieces:     utils.DivisionCeil(output.Info.Length, output.Info.PieceLength),
		PieceHashList: pieces,
		PieceLength:   output.Info.PieceLength,
	}
	return newTorrent, nil
}

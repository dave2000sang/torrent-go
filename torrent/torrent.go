package torrent

import (
	"bytes"
	"crypto/sha1"
	"errors"
	"fmt"
	"os"

	"github.com/jackpal/bencode-go"
)

// Torrent is a .torrent file
type Torrent struct {
	Announce   string
	InfoHash   [20]byte
	FileName   string
	FileLength string
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

func checkError(err error) {
	if err != nil {
		panic(err)
	}
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
	fmt.Println(tmp.Info.Name)
	return createTorrent(tmp)
}

// TODO - implement me!
func createTorrent(output bencodeOutput) (Torrent, error) {
	newTorrent := Torrent{}
	var infoHashes string = output.Info.Pieces
	// assert that infoHashes is multiple of 20
	if len(infoHashes)%20 != 0 {
		return Torrent{}, errors.New("torrent format error: info hash must be a multiple of 20 bytes")
	}
	// generate 20 byte SHA1 hash, to be sent to tracker server
	var buf bytes.Buffer
	bencode.Marshal(&buf, output.Info)
	fmt.Printf("%x", sha1.Sum([]byte(buf.String())))
	// fmt.Printf("%x", sha1.Sum([]byte("a")))

	return newTorrent, nil
}

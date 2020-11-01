package piece

import (
	"bytes"
	"encoding/binary"
	"errors"
	"crypto/sha1"
	"os"
)

// Piece represents a piece that is downloaded
type Piece struct {
	Index         int
	BlockIndex    int
	Blocks        []byte
	IsComplete    bool // if piece is finished downloading all its blocks
	IsDownloading bool // if started downloading piece
	Hash          []byte
}

// Blocksize for downloading pieces
const Blocksize = 16384 // Block size = 16KB

// NewPiece constructor
func NewPiece(index int, hash []byte) *Piece {
	return &Piece{Index: index, BlockIndex: 0, IsComplete: false, IsDownloading: false, Hash: hash}
}

// WriteToDisk writes piece to disk
func (piece *Piece) WriteToDisk(filename string, file *os.File, pieceLength int) error {
	if !piece.IsComplete {
		return errors.New("Error: piece is not complete downloading")
	}
	n, err := file.WriteAt(piece.Blocks, int64(piece.Index*pieceLength))
	if n != len(piece.Blocks) {
		return errors.New("Failed to write entire piece to file")
	}
	if err != nil {
		return err
	}
	return nil
}

// UpdatePieceWithBlock updates piece.Blocks[] using response message payload
func (piece *Piece) UpdatePieceWithBlock(payload []byte, requestMsg []byte) error {
	if len(payload) != Blocksize+8 {
		return errors.New("Error: piece does not match requested block size")
	}
	// pieceIndex := payload[:4]
	// pieceBegin := payload[4:8]
	pieceBlock := payload[8:]
	// Check if piece and begin block index match requested
	if !bytes.Equal(payload[:8], requestMsg[:8]) ||
		len(pieceBlock) != int(binary.BigEndian.Uint32(requestMsg[8:])) {
		return errors.New("ERROR: Peer sent piece doesn't match requested piece")
	}
	// Save block to Piece struct
	piece.Blocks = append(piece.Blocks, pieceBlock...)
	piece.BlockIndex += len(pieceBlock)
	piece.IsDownloading = true
	return nil
}

// Verify checks piece against SHA1 hash
func (piece *Piece) Verify() bool {
	curHash := sha1.Sum(piece.Blocks)
	return bytes.Equal(curHash[:], piece.Hash)
}


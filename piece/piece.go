package piece

import (
	"bytes"
	"encoding/binary"
	"errors"
)

// Piece represents a piece that is downloaded
type Piece struct {
	Index         int
	BlockIndex    int
	Blocks        []byte
	IsComplete    bool // if piece is finished downloading all its blocks
	IsDownloading bool // if started downloading piece
}

// Blocksize for downloading pieces
const Blocksize = 16384 // Block size = 16KB

// NewPiece constructor
func NewPiece(index int) *Piece {
	return &Piece{Index: index, BlockIndex: 0, IsComplete: false, IsDownloading: false}
}

// WriteToDisk writes piece to disk
func WriteToDisk(filepath string) {

}

// UpdatePieceWithBlock updates piece.Blocks[] using response message payload
func (piece *Piece) UpdatePieceWithBlock(payload []byte, requestMsg []byte) error {
	if len(payload) != Blocksize + 8 {
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

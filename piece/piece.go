package piece

// Piece represents a piece that is downloaded
type Piece struct {
	Index         int
	BlockIndex    int
	Blocks        []byte
	IsComplete    bool // if piece is finished downloading all its blocks
	IsDownloading bool // if started downloading piece
}

// NewPiece constructor
func NewPiece(index int) *Piece {
	return &Piece{Index: index, BlockIndex: 0, IsComplete: false, IsDownloading: false}
}

// WriteToDisk writes piece to disk
func WriteToDisk(filepath string) {

}

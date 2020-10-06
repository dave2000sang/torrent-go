package main

import "github.com/dave2000sang/torrent-go/torrent"

func checkError(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	file := "example_torrents/archlinux-2019.12.01-x86_64.iso.torrent"
	// fileData, err := ioutil.ReadFile(file)
	// checkError(err)
	// fmt.Print(bencode.Decode(fileData))
	torrent.ReadTorrentFile(file)
}

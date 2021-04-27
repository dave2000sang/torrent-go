package main

import (
	"log"
	"time"
	"torrent-go/client"
	"torrent-go/dht"
	"torrent-go/peer"
	"torrent-go/torrent"
)

// TEST flag
const TEST = true
// UseDHT flag
const UseDHT = true
// FoundPeerMin is the minimum number of peers for DHT to fetch
const FoundPeerMin = 30
// PeerConcurrentLimit is max number of goroutines to run concurrently
const PeerConcurrentLimit = 30

func main() {
	log.Println("TEST = ", TEST)
	log.Println("UseDHT = ", UseDHT)
	file := "example_torrents/ubuntu-20.04.1-desktop-amd64.iso.torrent"
	// Create a new Client
	curTorrent, err := torrent.ReadTorrentFile(file)
	if err != nil {
		panic(err)
	}
	client, err := client.NewClient(curTorrent)
	if err != nil {
		panic(err)
	}
	defer client.DownloadFile.Close()

	if UseDHT {
		// Get peers from DHT (trackerless client)
		// Start the DHT node
		myDHT := dht.NewDHT()
		if err := myDHT.Start(); err != nil {
			log.Fatal("Failed to start DHT node ", err)
		}
		// Listen for peers on dht.FoundPeers channel
		dhtLoop:
		for {
			select {
			case foundPeer := <-myDHT.FoundPeers:
				newPeer, _ := peer.NewPeer(foundPeer.IP, foundPeer.Port, client.TorrentFile.NumPieces)
				log.Println("FOUND PEER ", newPeer)
				client.AddNewPeer(newPeer)
				log.Println("TOTAL NUM PEERS:", len(client.PeerList))
				if len(client.PeerList) >= FoundPeerMin {
					myDHT.Stop()
					break dhtLoop
				}
			case <-myDHT.DoneBootstrapping:
				log.Println("Done bootstrapping")
				// Request peers with info hash
				myDHT.TriggerGetPeers(client.TorrentFile.InfoHash)
			}
		}
	} else {
		// Get peers list from tracker
		log.Println("Getting peers from tracker")
		client.ConnectTracker()		
	}

	log.Println("NUM PIECES: ", client.TorrentFile.NumPieces) // ~10000 for ubuntu image
	log.Println("NUM PEERS: ", len(client.PeerList))
	
	if TEST {
		if len(client.PeerList) > PeerConcurrentLimit {
			client.PeerList = client.PeerList[:PeerConcurrentLimit]
		}
	}

	startTime := time.Now()
	// Connect to peers and download file
	client.ConnectPeers(UseDHT)
	log.Println("elapsed time: ", time.Since(startTime))
}

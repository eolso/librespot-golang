package player

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/eolso/librespot-golang/Spotify"
	"github.com/eolso/librespot-golang/librespot/connection"
	"github.com/eolso/librespot-golang/librespot/mercury"
	"github.com/eolso/threadsafe"
	"log"
	"sync"
)

type Player struct {
	stream   connection.PacketStream
	mercury  *mercury.Client
	seq      uint32
	audioKey []byte
	channels *threadsafe.Map[uint16, *Channel]
	seqChans sync.Map
	nextChan uint16
}

func CreatePlayer(conn connection.PacketStream, client *mercury.Client) *Player {
	return &Player{
		stream:   conn,
		mercury:  client,
		channels: threadsafe.NewMap[uint16, *Channel](),
		seqChans: sync.Map{},
		//chanLock: sync.Mutex{},
		nextChan: 0,
	}
}

func (p *Player) LoadTrack(file *Spotify.AudioFile, trackId []byte) (*AudioFile, error) {
	return p.LoadTrackWithIdAndFormat(file.FileId, file.GetFormat(), trackId)
}

func (p *Player) LoadTrackWithIdAndFormat(fileId []byte, format Spotify.AudioFile_Format, trackId []byte) (*AudioFile, error) {
	// fmt.Printf("[player] Loading track audio key, fileId: %s, trackId: %s\n", utils.ConvertTo62(fileId), utils.ConvertTo62(trackId))

	// Allocate an AudioFile and a channel
	audioFile := newAudioFileWithIdAndFormat(fileId, format, p)

	// Start loading the audio key
	err := audioFile.loadKey(trackId)

	// Then start loading the audio itself
	audioFile.loadChunks()

	return audioFile, err
}

func (p *Player) loadTrackKey(trackId []byte, fileId []byte) ([]byte, error) {
	seqInt, seq := p.mercury.NextSeqWithInt()

	p.seqChans.Store(seqInt, make(chan []byte))

	req := buildKeyRequest(seq, trackId, fileId)
	err := p.stream.SendPacket(connection.PacketRequestKey, req)
	if err != nil {
		log.Println("Error while sending packet", err)
		return nil, err
	}

	channel, _ := p.seqChans.Load(seqInt)
	key := <-channel.(chan []byte)
	p.seqChans.Delete(seqInt)

	return key, nil
}

func (p *Player) AllocateChannel() *Channel {
	channel := NewChannel(p.nextChan, p.releaseChannel)
	p.nextChan++

	p.channels.Set(channel.num, channel)

	return channel
}

func (p *Player) HandleCmd(cmd byte, data []byte) {
	switch {
	case cmd == connection.PacketAesKey:
		// Audio key response
		dataReader := bytes.NewReader(data)
		var seqNum uint32
		binary.Read(dataReader, binary.BigEndian, &seqNum)

		if channel, ok := p.seqChans.Load(seqNum); ok {
			channel.(chan []byte) <- data[4:20]
		} else {
			fmt.Printf("[player] Unknown channel for audio key seqNum %d\n", seqNum)
		}

	case cmd == connection.PacketAesKeyError:
		// Audio key error
		fmt.Println("[player] Audio key error!")
		fmt.Printf("%x\n", data)

	case cmd == connection.PacketStreamChunkRes:
		// Audio data response
		var channel uint16
		dataReader := bytes.NewReader(data)
		binary.Read(dataReader, binary.BigEndian, &channel)

		// fmt.Printf("[player] Data on channel %d: %d bytes\n", channel, len(data[2:]))

		if val, ok := p.channels.Get(channel); ok {
			val.handlePacket(data[2:])
		} else {
			fmt.Printf("Unknown channel!\n")
		}
	}
}

func (p *Player) releaseChannel(channel *Channel) {
	p.channels.Delete(channel.num)
	// fmt.Printf("[player] Released channel %d\n", channel.num)
}

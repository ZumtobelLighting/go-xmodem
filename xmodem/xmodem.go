package xmodem

import (
	"bytes"
	"io"
	"log"
)

const SOH byte = 0x01
const STX byte = 0x02
const EOT byte = 0x04
const ACK byte = 0x06
const NAK byte = 0x15
const POLL byte = 0x43

const SHORT_PACKET_PAYLOAD_LEN = 128
const LONG_PACKET_PAYLOAD_LEN = 1024

func CRC16(data []byte) uint16 {
	var u16CRC uint16 = 0

	for _, character := range data {
		part := uint16(character)

		u16CRC = u16CRC ^ (part << 8)
		for i := 0; i < 8; i++ {
			if u16CRC&0x8000 > 0 {
				u16CRC = u16CRC<<1 ^ 0x1021
			} else {
				u16CRC = u16CRC << 1
			}
		}
	}

	return u16CRC
}

func CRC16Constant(data []byte, length int) uint16 {
	var u16CRC uint16 = 0

	for _, character := range data {
		part := uint16(character)

		u16CRC = u16CRC ^ (part << 8)
		for i := 0; i < 8; i++ {
			if u16CRC&0x8000 > 0 {
				u16CRC = u16CRC<<1 ^ 0x1021
			} else {
				u16CRC = u16CRC << 1
			}
		}
	}

	for c := 0; c < length-len(data); c++ {
		u16CRC = u16CRC ^ (0x04 << 8)
		for i := 0; i < 8; i++ {
			if u16CRC&0x8000 > 0 {
				u16CRC = u16CRC<<1 ^ 0x1021
			} else {
				u16CRC = u16CRC << 1
			}
		}
	}

	return u16CRC
}

func sendBlock(c io.ReadWriter, block uint8, data []byte, onek bool) error {
	var packetPayloadLen int
	if onek {
		packetPayloadLen = LONG_PACKET_PAYLOAD_LEN
	} else {
		packetPayloadLen = SHORT_PACKET_PAYLOAD_LEN
	}
	//send STX
	start := make([]byte, 1)
	if onek {
		start[0] = STX
	} else {
		start[0] = SOH
	}
	if _, err := c.Write(start); err != nil {
		return err
	}
	if _, err := c.Write([]byte{block}); err != nil {
		return err
	}
	if _, err := c.Write([]byte{255 - block}); err != nil {
		return err
	}

	//send data
	var toSend bytes.Buffer
	toSend.Write(data)
	for toSend.Len() < packetPayloadLen {
		toSend.Write([]byte{EOT})
	}

	sent := 0
	for sent < toSend.Len() {
		if n, err := c.Write(toSend.Bytes()[sent:]); err != nil {
			return err
		} else {
			sent += n
		}
	}

	//calc CRC
	u16CRC := CRC16Constant(data, packetPayloadLen)

	//send CRC
	if _, err := c.Write([]byte{uint8(u16CRC >> 8)}); err != nil {
		return err
	}
	if _, err := c.Write([]byte{uint8(u16CRC & 0x0FF)}); err != nil {
		return err
	}

	return nil
}

func ModemSend(c io.ReadWriter, data []byte, onek bool) error {
	oBuffer := make([]byte, 1)

	var packetPayloadLen int
	if onek {
		packetPayloadLen = LONG_PACKET_PAYLOAD_LEN
	} else {
		packetPayloadLen = SHORT_PACKET_PAYLOAD_LEN
	}

	if _, err := c.Read(oBuffer); err != nil {
		return err
	}

	if oBuffer[0] == POLL {
		var blocks int = len(data) / packetPayloadLen
		if len(data) > blocks*packetPayloadLen {
			blocks++
		}

		failed := 0
		var currentBlock int = 0
		for currentBlock < blocks && failed < 10 {
			if (currentBlock+1)*packetPayloadLen > len(data) {
				sendBlock(c, uint8(currentBlock+1), data[currentBlock*packetPayloadLen:], onek)
			} else {
				sendBlock(c, uint8(currentBlock+1), data[currentBlock*packetPayloadLen:(currentBlock+1)*packetPayloadLen], onek)
			}

			if _, err := c.Read(oBuffer); err != nil {
				return err
			}

			if oBuffer[0] == ACK {
				currentBlock++
			} else {
				failed++
			}
		}

		if _, err := c.Write([]byte{EOT}); err != nil {
			return err
		}
	}

	return nil
}

func ModemReceive(c io.ReadWriter) ([]byte, error) {
	var data bytes.Buffer
	oBuffer := make([]byte, 1)
	dBuffer := make([]byte, LONG_PACKET_PAYLOAD_LEN)

	log.Println("Before")

	// Start Connection
	if _, err := c.Write([]byte{POLL}); err != nil {
		return nil, err
	}

	log.Println("Write Poll")

	// Read Packets
	for {
		if _, err := c.Read(oBuffer); err != nil {
			return nil, err
		}
		pType := oBuffer[0]
		log.Println("PType:", pType)

		if pType == EOT {
			if _, err := c.Write([]byte{ACK}); err != nil {
				return nil, err
			}
			break
		}

		var packetSize int
		switch pType {
		case SOH:
			packetSize = SHORT_PACKET_PAYLOAD_LEN
		case STX:
			packetSize = LONG_PACKET_PAYLOAD_LEN
		}

		if _, err := c.Read(oBuffer); err != nil {
			return nil, err
		}
		packetCount := oBuffer[0]

		if _, err := c.Read(oBuffer); err != nil {
			return nil, err
		}
		inverseCount := oBuffer[0]

		if packetCount > inverseCount || inverseCount+packetCount != 255 {
			if _, err := c.Write([]byte{NAK}); err != nil {
				return nil, err
			}
			continue
		}

		received := 0
		var pData bytes.Buffer
		for received < packetSize {
			n, err := c.Read(dBuffer)
			if err != nil {
				return nil, err
			}

			received += n
			pData.Write(dBuffer[:n])
		}

		var crc uint16
		if _, err := c.Read(oBuffer); err != nil {
			return nil, err
		}
		crc = uint16(oBuffer[0])

		if _, err := c.Read(oBuffer); err != nil {
			return nil, err
		}
		crc <<= 8
		crc |= uint16(oBuffer[0])

		// Calculate CRC
		crcCalc := CRC16(pData.Bytes())
		if crcCalc == crc {
			data.Write(pData.Bytes())
			if _, err := c.Write([]byte{ACK}); err != nil {
				return nil, err
			}
		} else {
			if _, err := c.Write([]byte{NAK}); err != nil {
				return nil, err
			}
		}
	}

	return data.Bytes(), nil
}

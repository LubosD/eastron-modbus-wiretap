package main

import (
	"errors"
	"log"
	"sync"
	"syscall"
	"time"

	"github.com/goburrow/serial"
)

type ModbusRTU struct {
	Port     serial.Port
	BaudRate int // used to calculate character time
	Messages chan []byte

	stop  bool
	data  chan []byte
	mutex sync.Mutex
}

func (m *ModbusRTU) Start() {
	m.stop = false
	m.Messages = make(chan []byte, 10)
	m.data = make(chan []byte, 1)

	go m.readLoop()
	go m.readPump()
}

func (m *ModbusRTU) Stop() {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.Port.Close()

	m.stop = true
	close(m.data)
}

func (m *ModbusRTU) readPump() {
	for {
		buffer := make([]byte, 256)
		num, err := m.Port.Read(buffer)

		if err != nil {
			if val, ok := err.(syscall.Errno); ok && (val.Temporary() || val.Timeout()) {
				time.Sleep(10 * time.Millisecond)
				continue
			}

			log.Println("ModbusRTU readPump: read error: " + err.Error())
			break
		}

		m.mutex.Lock()

		if m.stop {
			m.mutex.Unlock()
			break
		}
		m.data <- buffer[:num]
		m.mutex.Unlock()
	}
}

func (m *ModbusRTU) readLoop() {
	// In RTU mode, message frames are separated by a silent interval of at least 3.5 character times.
	silentInterval := time.Duration(float64(time.Second/time.Duration(m.BaudRate)) * 3.5)

	buffer := make([]byte, 0, 256)

	var timer *time.Timer

	timer = time.NewTimer(time.Hour)

loop:
	for {
		select {
		case input, ok := <-m.data:
			if !ok {
				break loop
			}
			buffer = append(buffer, input...)

			if timer != nil {
				timer.Stop()
			}
			timer = time.NewTimer(silentInterval)

		case <-timer.C:
			if len(buffer) >= 4 {
				var crc crc
				crc.reset().pushBytes(buffer[:len(buffer)-2])

				rcvdChecksum := uint16(buffer[len(buffer)-1])<<8 | uint16(buffer[len(buffer)-2])

				if rcvdChecksum != crc.value() {
					log.Println("Incorrect checksum, dropping message")
				} else {
					m.Messages <- buffer
				}
			}
			buffer = make([]byte, 0, 256)
		}
	}
}

func (m *ModbusRTU) ReadPDU() (*AddressedPDU, error) {
	msg, ok := <-m.Messages

	if !ok {
		return nil, errors.New("ModbusRTU stopped")
	}

	if (msg[1] & 0x80) == 0x80 {
		var exceptionCode byte

		if len(msg) > 2 {
			exceptionCode = msg[2]
		}

		return nil, &ModbusError{
			FunctionCode:  msg[1] & 0x7f,
			ExceptionCode: exceptionCode,
		}
	}

	pdu := &AddressedPDU{
		ProtocolDataUnit: ProtocolDataUnit{
			FunctionCode: msg[1],
			Data:         msg[2 : len(msg)-2],
		},
		Slave: msg[0],
	}

	// log.Println("msg len:", len(msg), "payload len:", len(pdu.Data))

	return pdu, nil
}

func (m *ModbusRTU) WritePDU(pdu *AddressedPDU) {
	buffer := make([]byte, len(pdu.Data)+4)

	buffer[0] = pdu.Slave
	buffer[1] = pdu.FunctionCode

	copy(buffer[2:], pdu.Data)

	var crc crc
	crc.reset().pushBytes(buffer[:len(buffer)-2])

	buffer[len(buffer)-2] = crc.low
	buffer[len(buffer)-1] = crc.high

	m.Port.Write(buffer)
}

package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"math"
	"os/signal"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/goburrow/serial"
)

// https://www.eastroneurope.com/images/uploads/products/protocol/SDM630_MODBUS_Protocol.pdf
const (
	EastronPhase1Power uint16 = 0x0c
	EastronPhase2Power uint16 = 0x0e
	EastronPhase3Power uint16 = 0x10

	EastronImportKwh uint16 = 0x48
	EastronExportKwh uint16 = 0x4a
)

func main() {
	signal.Ignore(syscall.SIGHUP)

	portPtr := flag.String("port", "/dev/ttyUSB0", "Path to your RS485 device")
	baudRatePtr := flag.Int("baudRate", 9600, "Port baud rate")
	mqttServerPtr := flag.String("mqttServer", "tcp://127.0.0.1:1883", "MQTT server address")
	topicPtr := flag.String("topic", "smartmeter", "MQTT topic prefix")

	flag.Parse()

	mqttClient := connectMqtt(*mqttServerPtr, *topicPtr)

	port, err := serial.Open(&serial.Config{
		Address:  *portPtr,
		BaudRate: *baudRatePtr,
		DataBits: 8,
		Parity:   "N",
		StopBits: 1,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer port.Close()

	modbus := ModbusRTU{
		Port:     port,
		BaudRate: *baudRatePtr,
	}
	modbus.Start()

	// Uncomment to see raw RS485 comms
	/*
		for {
			pdu, err := modbus.ReadPDU()

			if err != nil {
				log.Println("Error: " + err.Error())
			} else {
				log.Printf("PDU slave=0x%x, function=%d, data=%s\n", pdu.Slave, pdu.FunctionCode, hex.EncodeToString(pdu.Data))
			}
		}
	*/

	wiretap := ModbusWiretap{
		RTU:         &modbus,
		TargetSlave: 1,
	}

	for {
		tapped, err := wiretap.Next()
		if err != nil {
			log.Fatalln("Bailing out on error:", err)
		}

		// Uncomment to see the communication
		//
		// log.Println("Req: ", tapped.Req)
		// log.Println("Resp:", tapped.Resp)

		if tapped.Req.FunctionCode != FuncCodeReadInputRegisters {
			log.Println("Ignoring unsupported function code...")
			continue
		}

		if len(tapped.Req.Data) != 4 {
			log.Println("Ignoring too short request data...")
			continue
		}

		var startRegister, regCount uint16

		startRegister = uint16(tapped.Req.Data[0])<<8 | uint16(tapped.Req.Data[1])
		regCount = (uint16(tapped.Req.Data[2])<<8 | uint16(tapped.Req.Data[3])) / 2

		for i := uint16(0); i < regCount; i++ {
			regNo := startRegister + i*2

			bits := binary.BigEndian.Uint32(tapped.Resp.Data[1+i*4 : 1+i*4+4])
			float := math.Float32frombits(bits)

			handleRegisterValue(regNo, float, mqttClient, *topicPtr)
		}
	}
}

func handleRegisterValue(register uint16, value float32, mqttClient mqtt.Client, topic string) {
	var fullTopicName string

	switch register {
	case EastronPhase1Power:
		log.Println("L1 power [W]:", value)
		fullTopicName = topic + "/power_l1"
	case EastronPhase2Power:
		log.Println("L2 power [W]:", value)
		fullTopicName = topic + "/power_l2"
	case EastronPhase3Power:
		log.Println("L3 power [W]:", value)
		fullTopicName = topic + "/power_l3"
	case EastronImportKwh:
		log.Println("Energy imported [kWh]:", value)
		fullTopicName = topic + "/energy_imported"
	case EastronExportKwh:
		log.Println("Energy exported [kWh]:", value)
		fullTopicName = topic + "/energy_exported"
	}

	if fullTopicName != "" {
		token := mqttClient.Publish(fullTopicName, 0, false, fmt.Sprint(value))

		if !token.Wait() {
			log.Fatalln("MQTT publish error:", token.Error())
		}
	}
}

func connectMqtt(address, topic string) mqtt.Client {
	opts := mqtt.NewClientOptions().AddBroker(address).SetClientID("eastron_wiretap")
	opts.SetKeepAlive(2 * time.Second)
	opts.SetPingTimeout(1 * time.Second)
	opts.SetWill(topic+"/status", "0", 0, true)

	c := mqtt.NewClient(opts)
	if token := c.Connect(); token.Wait() && token.Error() != nil {
		log.Fatalln("Failed to connect to MQTT server:", token.Error())
	}

	c.Publish(topic+"/status", 0, true, "1").Wait()

	return c
}

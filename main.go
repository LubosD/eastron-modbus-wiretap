package main

import (
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
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
	automasterPtr := flag.Bool("automaster", false, "Automatically become master if no other master is detected")
	slavePtr := flag.Int("slave", 1, "Slave number")

	flag.Parse()

	mqttClient := connectMqtt(*mqttServerPtr, *topicPtr)
	pushHomeAssistantConfig(mqttClient, *topicPtr)

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
		RTU:             &modbus,
		TargetSlave:     byte(*slavePtr),
		LastHeardMaster: time.Now(),
	}

	if *automasterPtr {
		go automasterLoop(&modbus, &wiretap)
	} else {
		go aliveCheck(&wiretap)
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
	opts.SetWill(topic+"/status", "offline", 0, true)
	opts.OnConnect = func(client mqtt.Client) {
		client.Publish(topic+"/status", 0, true, "online").Wait()
	}

	c := mqtt.NewClient(opts)
	if token := c.Connect(); token.Wait() && token.Error() != nil {
		log.Fatalln("Failed to connect to MQTT server:", token.Error())
	}

	return c
}

type HassAutoconfig struct {
	DeviceClass       string               `json:"dev_cla"`
	UnitOfMeasurement string               `json:"unit_of_meas"`
	Name              string               `json:"name"`
	StatusTopic       string               `json:"stat_t"`
	AvailabilityTopic string               `json:"avty_t"`
	UniqueID          string               `json:"uniq_id"`
	StateClass        string               `json:"stat_cla"`
	Device            HassAutoconfigDevice `json:"dev"`
}

type HassAutoconfigDevice struct {
	IDs  string `json:"ids"`
	Name string `json:"name"`
}

func pushHomeAssistantConfig(mqttClient mqtt.Client, topic string) {
	var autoconf HassAutoconfig

	hostname, _ := os.Hostname()

	autoconf.DeviceClass = "energy"
	autoconf.StateClass = "total_increasing"
	autoconf.UnitOfMeasurement = "kWh"
	autoconf.Name = "energy_imported"
	autoconf.AvailabilityTopic = topic + "/status"
	autoconf.StatusTopic = topic + "/energy_imported"
	autoconf.UniqueID = fmt.Sprint(topic, ".", hostname, ".", autoconf.Name)
	autoconf.Device.IDs = hostname
	autoconf.Device.Name = hostname

	jsonBytes, _ := json.Marshal(&autoconf)
	mqttClient.Publish("homeassistant/sensor/smartmeter_"+hostname+"/energy_imported/config", 0, true, string(jsonBytes)).Wait()

	autoconf.Name = "energy_exported"
	autoconf.StatusTopic = topic + "/energy_exported"
	autoconf.UniqueID = fmt.Sprint(topic, ".", hostname, ".", autoconf.Name)

	jsonBytes, _ = json.Marshal(&autoconf)
	mqttClient.Publish("homeassistant/sensor/smartmeter_"+hostname+"/energy_exported/config", 0, true, string(jsonBytes)).Wait()

	autoconf.DeviceClass = "power"
	autoconf.UnitOfMeasurement = "W"
	autoconf.StateClass = "measurement"

	for i := 1; i <= 3; i++ {
		autoconf.Name = fmt.Sprint("power_phase", i)
		autoconf.StatusTopic = fmt.Sprint(topic, "/power_l", i)
		autoconf.UniqueID = fmt.Sprint(topic, ".", hostname, ".", autoconf.Name)

		jsonBytes, _ = json.Marshal(&autoconf)
		mqttClient.Publish("homeassistant/sensor/smartmeter_"+hostname+"/"+autoconf.Name+"/config", 0, true, string(jsonBytes)).Wait()
	}
}

func automasterLoop(modbus *ModbusRTU, wiretap *ModbusWiretap) {
	// The inverter normally asks for data every 0.5 seconds.
	// Lets ask the Smartmeter every 5 seconds to minimize collision risks.

	for {
		time.Sleep(5 * time.Second)

		// We haven't seen the inverter during the last 5 seconds
		if time.Since(wiretap.LastHeardMaster) > 5*time.Second {
			log.Println("No master around, let's ask ourselves...")

			sendRequest(modbus, wiretap, EastronPhase1Power, 6)
			time.Sleep(500 * time.Millisecond)

			sendRequest(modbus, wiretap, EastronImportKwh, 4)
		}
	}
}

func aliveCheck(wiretap *ModbusWiretap) {
	for {
		time.Sleep(5 * time.Second)

		// Crash the process if we're not receiving any messages.
		// This may actually fix problems with the USB dongle driver on RPi.
		if time.Since(wiretap.LastHeardMaster) > time.Minute {
			log.Fatalln("No packets received within past 1 minute!")
		}
	}
}

func sendRequest(modbus *ModbusRTU, wiretap *ModbusWiretap, register, length uint16) {
	pdu := AddressedPDU{
		Slave: wiretap.TargetSlave,
		ProtocolDataUnit: ProtocolDataUnit{
			FunctionCode: FuncCodeReadInputRegisters,
			Data: []byte{
				byte(register >> 8),
				byte(register & 0xff),
				byte(length >> 8),
				byte(length & 0xff),
			},
		},
	}

	// log.Println("Sending request:", &pdu.ProtocolDataUnit)

	wiretap.SetLastReq(&pdu.ProtocolDataUnit)
	modbus.WritePDU(&pdu)
}

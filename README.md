# Modbus Wiretap for Eastron SDM630

## Purpose

I have a Deye inverter connected to an Eastron SDM630 smartmeter. I wanted to read live detailed (per-phase) data from the smartmeter and push this data into my MQTT server, but the smartmeter has only one port, which is already connected to the inverter.

As Modbus RTU is a protocol that allows only for a single master node on the RS485 network (i.e. you cannot have multiple devices asking the smartmeter), I decided to wiretap the connection and passively listen for exchanged messages.

## Limitations

* This app is passive and doesn't send any requests to the smartmeter. If the inverter is not running, no requests are sent to the slave (the smartmeter), hence the app won't publish any data.
* This app has only been tested with my Deye 8kW inverter and only supports the registers read by this model (current wattage and total energy import/export).
* This app is only intended for the Eastron SDM630 smartmeter. Register numbers will differ between smartmeter manufacturers.
* This app is designed for PC-like systems (e.g. the Raspberry Pi). If you need something for ESP8266/ESP32, you need to look elsewhere.

## Build

You need to [download and install Go](https://go.dev/dl/). Then you can build the project by running:

```
go build
```

## Usage

Given that Deye inverters connect the A and B wires to 2 different RJ45 pins, creating a physical wiretap is pretty straightforward.

App usage example (using default options):

```
./eastron_wiretap -port /dev/ttyUSB0 -baudRate 9600 -mqttServer 127.0.01 -topic smartmeter
```

## Acknowledgements

This app uses some utility code written by Quoc-Viet Nguyen taken from [goburrow/modbus](https://github.com/goburrow/modbus).

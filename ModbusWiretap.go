package main

import "time"

type ModbusWiretap struct {
	RTU             *ModbusRTU
	TargetSlave     byte
	LastHeardMaster time.Time

	LastReq *ProtocolDataUnit
}

type WiretapReqResp struct {
	Req, Resp *ProtocolDataUnit
}

func (w *ModbusWiretap) SetLastReq(r *ProtocolDataUnit) {
	w.LastReq = r
}

func (w *ModbusWiretap) Next() (*WiretapReqResp, error) {

	for {
		pdu, err := w.RTU.ReadPDU()

		if err != nil {
			if _, ok := err.(*ModbusError); !ok {
				// Fail with non-modbus errors
				return nil, err
			}

			continue
		}

		if pdu.Slave != w.TargetSlave {
			continue
		}

		if len(pdu.Data) == 4 {
			// Looks like a request
			w.LastReq = &pdu.ProtocolDataUnit
			w.LastHeardMaster = time.Now()
		} else if w.LastReq != nil {
			if w.LastReq.FunctionCode == pdu.FunctionCode {
				req := w.LastReq
				w.LastReq = nil

				return &WiretapReqResp{
					Req:  req,
					Resp: &pdu.ProtocolDataUnit,
				}, nil
			}
		}

	}
}

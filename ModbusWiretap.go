package main

type ModbusWiretap struct {
	RTU         *ModbusRTU
	TargetSlave byte
}

type WiretapReqResp struct {
	Req, Resp *ProtocolDataUnit
}

func (w *ModbusWiretap) Next() (*WiretapReqResp, error) {
	var rv WiretapReqResp

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
			rv.Req = &pdu.ProtocolDataUnit
		} else if rv.Req != nil {
			if rv.Req.FunctionCode == pdu.FunctionCode {
				rv.Resp = &pdu.ProtocolDataUnit
				return &rv, nil
			}
		}

	}
}

package proxy

import "net"

// RelayUDP 在分帧的 workConn 与目标 UDP 连接之间双向转发。
// mirror 非空时，同一份字节数会同时计入 mirror（用于单条会话的连接记录统计）。
func RelayUDP(workConn net.Conn, target net.Conn, counter, mirror *Counter) {
	done := make(chan struct{}, 2)

	addIn := func(n int64) {
		if counter != nil {
			counter.AddIn(n)
		}
		if mirror != nil {
			mirror.AddIn(n)
		}
	}
	addOut := func(n int64) {
		if counter != nil {
			counter.AddOut(n)
		}
		if mirror != nil {
			mirror.AddOut(n)
		}
	}

	// workConn(分帧) -> target
	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, MaxPacket)
		for {
			n, err := ReadFrame(workConn, buf)
			if err != nil {
				break
			}
			if _, err := target.Write(buf[:n]); err != nil {
				break
			}
			addIn(int64(n))
		}
		_ = workConn.Close()
		_ = target.Close()
	}()

	// target -> workConn(分帧)
	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, MaxPacket)
		for {
			n, err := target.Read(buf)
			if err != nil {
				break
			}
			if err := WriteFrame(workConn, buf[:n]); err != nil {
				break
			}
			addOut(int64(n))
		}
		_ = workConn.Close()
		_ = target.Close()
	}()

	<-done
}

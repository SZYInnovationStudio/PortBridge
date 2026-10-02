package proxy

import "net"

// RelayUDP 在分帧的 workConn 与目标 UDP 连接之间双向转发
func RelayUDP(workConn net.Conn, target net.Conn, counter *Counter) {
	done := make(chan struct{}, 2)

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
			if counter != nil {
				counter.AddIn(int64(n))
			}
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
			if counter != nil {
				counter.AddOut(int64(n))
			}
		}
		_ = workConn.Close()
		_ = target.Close()
	}()

	<-done
}

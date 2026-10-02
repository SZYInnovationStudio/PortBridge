package server

import (
	"time"

	"github.com/gin-gonic/gin"

	"portbridge/internal/loghub"
)

// handleWSEvents Web UI 实时事件与日志推送
func (s *Server) handleWSEvents(c *gin.Context) {
	conn, err := agentUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	evCh, cancelEv := s.events.Subscribe()
	defer cancelEv()
	lgCh, cancelLg := loghub.Default.Subscribe()
	defer cancelLg()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	writeJSON := func(v any) bool {
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return conn.WriteJSON(v) == nil
	}

	// 初始推送最近日志快照
	for _, e := range loghub.Default.Recent() {
		if !writeJSON(gin.H{"type": "log", "data": e}) {
			return
		}
	}

	for {
		select {
		case <-done:
			return
		case e, ok := <-evCh:
			if !ok || !writeJSON(gin.H{"type": e.Type, "data": e.Data, "ts": e.Ts}) {
				return
			}
		case l, ok := <-lgCh:
			if !ok || !writeJSON(gin.H{"type": "log", "data": l}) {
				return
			}
		}
	}
}

package server

import (
	"errors"
	"fmt"
	"log"
	"time"

	"gorm.io/gorm"

	"portbridge/internal/loghub"
	"portbridge/internal/model"
	"portbridge/internal/protocol"
)

// applyProxy 应用规则到监听层
func (s *Server) applyProxy(p *model.Proxy) {
	if err := s.pm.Apply(p); err != nil {
		s.pm.SetLastError(p.Name, err.Error())
		loghub.Default.Publish("error", fmt.Sprintf("应用规则 %s 失败: %v", p.Name, err))
		return
	}
	s.pm.SetLastError(p.Name, "")
}

// handleProxyReport 处理原站端上报的本地规则（LWW 合并 + 全量回下发）
func (s *Server) handleProxyReport(nodeName string, report *protocol.ProxyReport) {
	seen := make(map[string]bool, len(report.Proxies))

	for i := range report.Proxies {
		spec := report.Proxies[i]
		if spec.Name == "" {
			continue
		}
		seen[spec.Name] = true

		var existing model.Proxy
		err := s.db.Where("name = ?", spec.Name).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			p := specToProxy(spec, nodeName)
			if p.Origin == "" {
				p.Origin = model.OriginClient
			}
			if err := s.db.Create(p).Error; err != nil {
				log.Printf("[server] 创建规则 %s 失败: %v", spec.Name, err)
				continue
			}
			s.applyProxy(p)
			continue
		}
		if err != nil {
			continue
		}
		// LWW：以配置版本号为准（UpdatedAt 会被 GORM 在每次保存时刷新，不能用于比较）
		if spec.Version > existing.Version {
			applySpecToProxy(spec, &existing)
			existing.NodeName = nodeName
			if err := s.db.Save(&existing).Error; err != nil {
				log.Printf("[server] 更新规则 %s 失败: %v", spec.Name, err)
				continue
			}
			s.applyProxy(&existing)
		}
	}

	// 原站端本地创建/管理的规则若已不存在，则视为删除
	var locals []model.Proxy
	if err := s.db.Where("node_name = ? AND origin = ?", nodeName, model.OriginClient).Find(&locals).Error; err == nil {
		for i := range locals {
			if !seen[locals[i].Name] {
				s.db.Delete(&locals[i])
				s.pm.Remove(locals[i].Name)
				loghub.Default.Publish("info", "规则已删除: "+locals[i].Name)
			}
		}
	}

	s.pushNodeSync(nodeName)
}

// handleProxyChange 处理原站端通过 UI 发起的规则增删改
func (s *Server) handleProxyChange(nodeName string, msgType protocol.MsgType, spec *protocol.ProxySpec) (bool, string) {
	if spec == nil || spec.Name == "" {
		return false, "规则名不能为空"
	}

	if msgType == protocol.MsgProxyDelete {
		var p model.Proxy
		if err := s.db.Where("name = ?", spec.Name).First(&p).Error; err != nil {
			return true, ""
		}
		if p.NodeName != "" && p.NodeName != nodeName {
			return false, "规则属于其它节点"
		}
		s.db.Delete(&p)
		s.pm.Remove(p.Name)
		s.broadcastProxyChange(p.Name)
		return true, ""
	}

	var p model.Proxy
	err := s.db.Where("name = ?", spec.Name).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		np := specToProxy(*spec, nodeName)
		if np.Origin == "" {
			np.Origin = model.OriginClient
		}
		if err := s.db.Create(np).Error; err != nil {
			return false, err.Error()
		}
		s.applyProxy(np)
		s.broadcastProxyChange(np.Name)
		return true, ""
	}
	if err != nil {
		return false, err.Error()
	}
	applySpecToProxy(*spec, &p)
	p.NodeName = nodeName
	if err := s.db.Save(&p).Error; err != nil {
		return false, err.Error()
	}
	s.applyProxy(&p)
	s.broadcastProxyChange(p.Name)
	return true, ""
}

// handleTrafficReport 处理原站端上报的流量增量
func (s *Server) handleTrafficReport(report *protocol.TrafficReport) {
	now := time.Now()
	for _, it := range report.Items {
		if it.BytesIn == 0 && it.BytesOut == 0 && it.Conns == 0 {
			continue
		}
		s.db.Model(&model.Proxy{}).Where("name = ?", it.ProxyName).Updates(map[string]any{
			"traffic_in":  gorm.Expr("traffic_in + ?", it.BytesIn),
			"traffic_out": gorm.Expr("traffic_out + ?", it.BytesOut),
			"total_conns": gorm.Expr("total_conns + ?", it.Conns),
		})
		var p model.Proxy
		if err := s.db.Where("name = ?", it.ProxyName).First(&p).Error; err == nil {
			s.db.Create(&model.TrafficStat{
				ProxyID: p.ID, ProxyName: it.ProxyName,
				BytesIn: it.BytesIn, BytesOut: it.BytesOut, Conns: int(it.Conns), BucketAt: now,
			})
		}
	}
}

// disableProxy 停用规则（如流量超限）
func (s *Server) disableProxy(p *model.Proxy, reason string) {
	s.db.Model(&model.Proxy{}).Where("id = ?", p.ID).Updates(map[string]any{
		"enabled": false,
		"version": p.Version + 1, // 自动停用也是真实状态变更，需同步到对端
	})
	s.pm.Remove(p.Name)
	s.events.Broadcast("proxy_status", map[string]any{"name": p.Name, "enabled": false, "reason": reason})
	if p.NodeName != "" {
		s.pushNodeSync(p.NodeName)
	}
}

// pushNodeSync 向指定节点下发其全部规则
func (s *Server) pushNodeSync(nodeName string) {
	var proxies []model.Proxy
	if err := s.db.Where("node_name = ?", nodeName).Find(&proxies).Error; err != nil {
		return
	}
	specs := make([]protocol.ProxySpec, 0, len(proxies))
	for i := range proxies {
		specs = append(specs, proxyToSpec(&proxies[i]))
	}
	msg, err := protocol.NewMessage(protocol.MsgProxySync, protocol.ProxyReport{Proxies: specs})
	if err != nil {
		return
	}
	if err := s.hub.Send(nodeName, msg); err != nil {
		// 节点离线时忽略
		return
	}
}

// broadcastProxyChange 推送规则变更到 Web UI 与相关节点
func (s *Server) broadcastProxyChange(name string) {
	var p model.Proxy
	if err := s.db.Where("name = ?", name).First(&p).Error; err != nil {
		return
	}
	s.events.Broadcast("proxy_change", proxyToSpec(&p))
	if p.NodeName != "" {
		s.pushNodeSync(p.NodeName)
	}
}

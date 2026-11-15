package server

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// RateLimiter 速率限制器
type RateLimiter struct {
	requests map[string]*clientRate
	mu       sync.RWMutex

	// 配置
	maxRequestsPerSecond int           // 每秒最大请求数
	maxRequestsPerMinute int           // 每分钟最大请求数
	banDuration          time.Duration // 封禁时长
	cleanupInterval      time.Duration // 清理间隔
}

// clientRate 客户端速率记录
type clientRate struct {
	secondCount int       // 当前秒请求数
	minuteCount int       // 当前分钟请求数
	lastSecond  time.Time // 上次秒级计数时间
	lastMinute  time.Time // 上次分钟计数时间
	bannedUntil time.Time // 封禁到期时间
}

// NewRateLimiter 创建速率限制器
func NewRateLimiter(maxPerSecond, maxPerMinute int, banDuration time.Duration) *RateLimiter {
	rl := &RateLimiter{
		requests:             make(map[string]*clientRate),
		maxRequestsPerSecond: maxPerSecond,
		maxRequestsPerMinute: maxPerMinute,
		banDuration:          banDuration,
		cleanupInterval:      5 * time.Minute,
	}

	// 启动清理协程
	go rl.cleanup()

	return rl
}

// Allow 检查是否允许请求
func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	rate, exists := rl.requests[ip]

	if !exists {
		rl.requests[ip] = &clientRate{
			secondCount: 1,
			minuteCount: 1,
			lastSecond:  now,
			lastMinute:  now,
		}
		return true
	}

	// 检查是否被封禁
	if now.Before(rate.bannedUntil) {
		return false
	}

	// 重置秒级计数
	if now.Sub(rate.lastSecond) >= time.Second {
		rate.secondCount = 0
		rate.lastSecond = now
	}

	// 重置分钟计数
	if now.Sub(rate.lastMinute) >= time.Minute {
		rate.minuteCount = 0
		rate.lastMinute = now
	}

	rate.secondCount++
	rate.minuteCount++

	// 检查是否超限
	if rate.secondCount > rl.maxRequestsPerSecond || rate.minuteCount > rl.maxRequestsPerMinute {
		rate.bannedUntil = now.Add(rl.banDuration)
		log.Printf("⚠️ IP %s 因请求过于频繁被暂时封禁 %v", ip, rl.banDuration)
		return false
	}

	return true
}

// IsBanned 检查 IP 是否被封禁
func (rl *RateLimiter) IsBanned(ip string) bool {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	rate, exists := rl.requests[ip]
	if !exists {
		return false
	}

	return time.Now().Before(rate.bannedUntil)
}

// cleanup 清理过期记录
func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(rl.cleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		rl.mu.Lock()
		now := time.Now()
		for ip, rate := range rl.requests {
			// 如果超过 10 分钟没有请求，删除记录
			if now.Sub(rate.lastMinute) > 10*time.Minute && now.After(rate.bannedUntil) {
				delete(rl.requests, ip)
			}
		}
		rl.mu.Unlock()
	}
}

// --- 来源验证 ---

// OriginChecker 来源验证器
type OriginChecker struct {
	allowedOrigins map[string]bool
}

// NewOriginChecker 创建来源验证器
func NewOriginChecker(origins []string) *OriginChecker {
	oc := &OriginChecker{
		allowedOrigins: make(map[string]bool),
	}

	for _, origin := range origins {
		origin = strings.TrimSpace(origin)
		if origin == "*" {
			continue // Credential-bearing browser sockets never allow wildcard origins.
		}
		oc.allowedOrigins[strings.ToLower(origin)] = true
	}

	return oc
}

// Check 检查来源是否允许
func (oc *OriginChecker) Check(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// 没有 Origin 头，可能是同源请求或本地客户端
		return true
	}

	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return (u.Scheme == scheme && strings.EqualFold(u.Host, r.Host)) || oc.allowedOrigins[strings.ToLower(origin)]
}

// --- IP 白名单/黑名单 ---

// IPFilter IP 过滤器
type IPFilter struct {
	whitelist map[string]bool // 白名单
	blacklist map[string]bool // 黑名单
	mu        sync.RWMutex
}

// NewIPFilter 创建 IP 过滤器
func NewIPFilter() *IPFilter {
	return &IPFilter{
		whitelist: make(map[string]bool),
		blacklist: make(map[string]bool),
	}
}

// AddToWhitelist 添加到白名单
func (f *IPFilter) AddToWhitelist(ip string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.whitelist[ip] = true
}

// AddToBlacklist 添加到黑名单
func (f *IPFilter) AddToBlacklist(ip string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blacklist[ip] = true
}

// RemoveFromBlacklist 从黑名单移除
func (f *IPFilter) RemoveFromBlacklist(ip string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.blacklist, ip)
}

// IsAllowed 检查 IP 是否允许
func (f *IPFilter) IsAllowed(ip string) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()

	// 如果有白名单且不在白名单中，拒绝
	if len(f.whitelist) > 0 && !f.whitelist[ip] {
		return false
	}

	// 如果在黑名单中，拒绝
	if f.blacklist[ip] {
		return false
	}

	return true
}

// --- 辅助函数 ---

func extractRemoteIP(remoteAddr string) string {
	ip, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return strings.TrimSpace(remoteAddr)
	}
	if parsed := net.ParseIP(ip); parsed != nil {
		return parsed.String()
	}
	return ip
}

// ClientIPResolver 仅在直连对端属于可信反向代理时才解析转发头
type ClientIPResolver struct {
	trustedNets []*net.IPNet
	trustedIPs  map[string]bool
}

// NewClientIPResolver 创建客户端 IP 解析器（支持单 IP 与 CIDR）
func NewClientIPResolver(trustedProxies []string) *ClientIPResolver {
	r := &ClientIPResolver{
		trustedIPs: make(map[string]bool),
	}
	for _, entry := range trustedProxies {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if _, cidr, err := net.ParseCIDR(entry); err == nil {
			r.trustedNets = append(r.trustedNets, cidr)
			continue
		}
		if ip := net.ParseIP(entry); ip != nil {
			r.trustedIPs[ip.String()] = true
		}
	}
	return r
}

func (cr *ClientIPResolver) isTrusted(ipStr string) bool {
	if cr == nil || (len(cr.trustedIPs) == 0 && len(cr.trustedNets) == 0) {
		return false
	}
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	if cr.trustedIPs[ip.String()] {
		return true
	}
	for _, cidr := range cr.trustedNets {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// Resolve 获取真实客户端 IP；仅当 RemoteAddr 属于可信代理时才信任 X-Forwarded-For / X-Real-IP
func (cr *ClientIPResolver) Resolve(r *http.Request) string {
	remoteIP := extractRemoteIP(r.RemoteAddr)
	if !cr.isTrusted(remoteIP) {
		return remoteIP
	}

	if xffValues := r.Header.Values("X-Forwarded-For"); len(xffValues) > 0 {
		combined := strings.Join(xffValues, ",")
		parts := strings.Split(combined, ",")
		var leftmostValid string
		// 从右向左查找第一个非可信代理 IP，防止客户端伪造前置 X-Forwarded-For 项
		for i := len(parts) - 1; i >= 0; i-- {
			candidate := strings.TrimSpace(parts[i])
			parsed := net.ParseIP(candidate)
			if parsed == nil {
				continue
			}
			canonical := parsed.String()
			leftmostValid = canonical
			if !cr.isTrusted(canonical) {
				return canonical
			}
		}
		if leftmostValid != "" {
			return leftmostValid
		}
	}

	if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
		if parsed := net.ParseIP(realIP); parsed != nil {
			return parsed.String()
		}
	}

	return remoteIP
}

// GetClientIP 默认仅从连接对端 RemoteAddr 提取客户端 IP，不信任未经鉴权的代理头
func GetClientIP(r *http.Request) string {
	return extractRemoteIP(r.RemoteAddr)
}

// --- 消息速率限制 ---

const (
	limiterSweepInterval = 5 * time.Minute
	limiterIdleTTL       = 10 * time.Minute
)

// MessageRateLimiter 消息速率限制器（针对已连接的客户端）
type MessageRateLimiter struct {
	limits map[string]*messageRate
	mu     sync.RWMutex

	maxMessagesPerSecond int
	warningThreshold     int // 警告阈值
	lastSweep            time.Time
}

type messageRate struct {
	count      int
	lastReset  time.Time
	lastActive time.Time
	warnings   int // 警告次数
}

// NewMessageRateLimiter 创建消息速率限制器
func NewMessageRateLimiter(maxPerSecond int) *MessageRateLimiter {
	return &MessageRateLimiter{
		limits:               make(map[string]*messageRate),
		maxMessagesPerSecond: maxPerSecond,
		warningThreshold:     maxPerSecond / 2,
		lastSweep:            time.Now(),
	}
}

// AllowMessage 检查是否允许发送消息
func (ml *MessageRateLimiter) AllowMessage(clientID string) (allowed, warning bool) {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	now := time.Now()
	if now.Sub(ml.lastSweep) >= limiterSweepInterval {
		ml.sweepLocked(now)
	}

	rate, exists := ml.limits[clientID]

	if !exists {
		ml.limits[clientID] = &messageRate{
			count:      1,
			lastReset:  now,
			lastActive: now,
		}
		return true, false
	}

	rate.lastActive = now

	// 如果超过 1 秒，重置计数
	if now.Sub(rate.lastReset) >= time.Second {
		rate.count = 1
		rate.lastReset = now
		return true, false
	}

	rate.count++

	// 超过限制
	if rate.count > ml.maxMessagesPerSecond {
		rate.warnings++
		return false, true
	}

	// 接近限制，发出警告
	if rate.count > ml.warningThreshold {
		return true, true
	}

	return true, false
}

func (ml *MessageRateLimiter) sweepLocked(now time.Time) {
	ml.lastSweep = now
	for id, rate := range ml.limits {
		if now.Sub(rate.lastActive) > limiterIdleTTL {
			delete(ml.limits, id)
		}
	}
}

// GetWarningCount 获取警告次数
func (ml *MessageRateLimiter) GetWarningCount(clientID string) int {
	ml.mu.RLock()
	defer ml.mu.RUnlock()

	rate, exists := ml.limits[clientID]
	if !exists {
		return 0
	}
	return rate.warnings
}

// ClearRateLimit 清除速率限制
func (ml *MessageRateLimiter) ClearRateLimit(clientID string) {
	ml.mu.Lock()
	defer ml.mu.Unlock()
	delete(ml.limits, clientID)
}

// --- 聊天消息速率限制 ---

// ChatRateLimiter 聊天消息速率限制器
type ChatRateLimiter struct {
	limits map[string]*chatRate
	mu     sync.RWMutex

	maxPerSecond int
	maxPerMinute int
	cooldown     time.Duration
	lastSweep    time.Time
}

type chatRate struct {
	secondCount   int
	minuteCount   int
	lastSecond    time.Time
	lastMinute    time.Time
	cooldownUntil time.Time
}

// NewChatRateLimiter 创建聊天消息速率限制器
func NewChatRateLimiter(maxPerSecond, maxPerMinute int, cooldown time.Duration) *ChatRateLimiter {
	return &ChatRateLimiter{
		limits:       make(map[string]*chatRate),
		maxPerSecond: maxPerSecond,
		maxPerMinute: maxPerMinute,
		cooldown:     cooldown,
		lastSweep:    time.Now(),
	}
}

// AllowChat 检查是否允许发送聊天消息
func (cl *ChatRateLimiter) AllowChat(clientID string) (allowed bool, reason string) {
	cl.mu.Lock()
	defer cl.mu.Unlock()

	now := time.Now()
	if now.Sub(cl.lastSweep) >= limiterSweepInterval {
		cl.sweepLocked(now)
	}

	rate, exists := cl.limits[clientID]

	if !exists {
		cl.limits[clientID] = &chatRate{
			secondCount: 1,
			minuteCount: 1,
			lastSecond:  now,
			lastMinute:  now,
		}
		return true, ""
	}

	// 检查是否在冷却期
	if now.Before(rate.cooldownUntil) {
		remaining := int(rate.cooldownUntil.Sub(now).Seconds()) + 1 // +1 以避免显示 0 秒
		return false, fmt.Sprintf("章鱼哥已上线，请保持安静 %d 秒", remaining)
	}

	// 重置秒级计数
	if now.Sub(rate.lastSecond) >= time.Second {
		rate.secondCount = 0
		rate.lastSecond = now
	}

	// 重置分钟计数
	if now.Sub(rate.lastMinute) >= time.Minute {
		rate.minuteCount = 0
		rate.lastMinute = now
	}

	rate.secondCount++
	rate.minuteCount++

	// 检查秒级限制
	if rate.secondCount > cl.maxPerSecond {
		rate.cooldownUntil = now.Add(cl.cooldown)
		return false, "派大星正在接管键盘..."
	}

	// 检查分钟限制
	if rate.minuteCount > cl.maxPerMinute {
		rate.cooldownUntil = now.Add(cl.cooldown)
		return false, "不要着急，休息，休息一会~"
	}

	return true, ""
}

func (cl *ChatRateLimiter) sweepLocked(now time.Time) {
	cl.lastSweep = now
	for id, rate := range cl.limits {
		if now.Sub(rate.lastMinute) > limiterIdleTTL && now.After(rate.cooldownUntil) {
			delete(cl.limits, id)
		}
	}
}

// ClearRateLimit 清除速率限制
func (cl *ChatRateLimiter) ClearRateLimit(clientID string) {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	delete(cl.limits, clientID)
}

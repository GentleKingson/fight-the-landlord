package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
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
	ctx                  context.Context
	cancel               context.CancelFunc
	workers              sync.WaitGroup
	closeOnce            sync.Once
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
	return NewRateLimiterWithContext(context.Background(), maxPerSecond, maxPerMinute, banDuration)
}

// NewRateLimiterWithContext creates a limiter whose cleanup worker is owned by
// the supplied runtime context.
func NewRateLimiterWithContext(ctx context.Context, maxPerSecond, maxPerMinute int, banDuration time.Duration) *RateLimiter {
	return newRateLimiter(ctx, maxPerSecond, maxPerMinute, banDuration, 5*time.Minute)
}

func newRateLimiter(parent context.Context, maxPerSecond, maxPerMinute int, banDuration, cleanupInterval time.Duration) *RateLimiter {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent) //nolint:gosec // Close owns cancellation and waits for the cleanup worker.
	rl := &RateLimiter{
		requests:             make(map[string]*clientRate),
		maxRequestsPerSecond: maxPerSecond,
		maxRequestsPerMinute: maxPerMinute,
		banDuration:          banDuration,
		cleanupInterval:      cleanupInterval,
		ctx:                  ctx,
		cancel:               cancel,
	}

	rl.workers.Add(1)
	go func() {
		defer rl.workers.Done()
		rl.cleanup()
	}()

	return rl
}

// Close stops the cleanup worker and waits for it to exit. It is idempotent.
func (rl *RateLimiter) Close() error {
	if rl == nil {
		return nil
	}
	rl.closeOnce.Do(func() {
		if rl.cancel != nil {
			rl.cancel()
		}
		rl.workers.Wait()
	})
	return nil
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

	for {
		select {
		case <-ticker.C:
			rl.mu.Lock()
			now := time.Now()
			for ip, rate := range rl.requests {
				// 如果超过 10 分钟没有请求，删除记录
				if now.Sub(rate.lastMinute) > 10*time.Minute && now.After(rate.bannedUntil) {
					delete(rl.requests, ip)
				}
			}
			rl.mu.Unlock()
		case <-rl.ctx.Done():
			return
		}
	}
}

// --- 来源验证 ---

// OriginChecker 来源验证器
type OriginChecker struct {
	allowedOrigins map[string]bool
	allowAll       bool
}

// NewOriginChecker 创建来源验证器
func NewOriginChecker(origins []string) *OriginChecker {
	oc := &OriginChecker{
		allowedOrigins: make(map[string]bool),
	}

	for _, origin := range origins {
		if origin == "*" {
			oc.allowAll = true
			return oc
		}
		oc.allowedOrigins[strings.ToLower(origin)] = true
	}

	return oc
}

// Check 检查来源是否允许
func (oc *OriginChecker) Check(r *http.Request) bool {
	if oc.allowAll {
		return true
	}

	origin := r.Header.Get("Origin")
	if origin == "" {
		// 没有 Origin 头，可能是同源请求或本地客户端
		return true
	}

	return oc.allowedOrigins[strings.ToLower(origin)]
}

func validateOriginPolicy(environment string, origins []string) error {
	if !strings.EqualFold(strings.TrimSpace(environment), "production") {
		return nil
	}
	if len(origins) == 0 {
		return errors.New("production requires at least one allowed origin")
	}
	for _, origin := range origins {
		if strings.TrimSpace(origin) == "*" {
			return errors.New("production does not allow wildcard Origin")
		}
	}
	return nil
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

// ClientIPResolver only accepts forwarding headers from explicitly trusted
// direct peers. It walks X-Forwarded-For from the nearest hop toward the
// client, stopping at the first untrusted address.
type ClientIPResolver struct {
	trusted []netip.Prefix
}

func NewClientIPResolver(cidrs []string) (*ClientIPResolver, error) {
	resolver := &ClientIPResolver{trusted: make([]netip.Prefix, 0, len(cidrs))}
	for _, value := range cidrs {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy CIDR %q: %w", value, err)
		}
		resolver.trusted = append(resolver.trusted, prefix.Masked())
	}
	return resolver, nil
}

func (resolver *ClientIPResolver) Resolve(r *http.Request) string {
	remoteText, remoteIP := remoteClientIP(r.RemoteAddr)
	if resolver == nil || !remoteIP.IsValid() || !resolver.isTrusted(remoteIP) {
		return remoteText
	}

	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		parts := strings.Split(forwarded, ",")
		addresses := make([]netip.Addr, len(parts))
		for index, part := range parts {
			address, err := netip.ParseAddr(strings.TrimSpace(part))
			if err != nil {
				return remoteText
			}
			addresses[index] = address.Unmap()
		}
		for index := len(addresses) - 1; index >= 0; index-- {
			if !resolver.isTrusted(addresses[index]) {
				return addresses[index].String()
			}
		}
		if len(addresses) > 0 {
			return addresses[0].String()
		}
	}

	if value := strings.TrimSpace(r.Header.Get("X-Real-IP")); value != "" {
		if address, err := netip.ParseAddr(value); err == nil {
			return address.Unmap().String()
		}
	}
	return remoteText
}

func (resolver *ClientIPResolver) isTrusted(address netip.Addr) bool {
	address = address.Unmap()
	for _, prefix := range resolver.trusted {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func remoteClientIP(remoteAddr string) (string, netip.Addr) {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	address, err := netip.ParseAddr(strings.TrimSpace(host))
	if err != nil {
		return host, netip.Addr{}
	}
	address = address.Unmap()
	return address.String(), address
}

// GetClientIP is the secure direct-connection default. Production servers use
// their configured ClientIPResolver when trusted proxies are present.
func GetClientIP(r *http.Request) string {
	remote, _ := remoteClientIP(r.RemoteAddr)
	return remote
}

// --- 消息速率限制 ---

// MessageRateLimiter 消息速率限制器（针对已连接的客户端）
type MessageRateLimiter struct {
	limits map[string]*messageRate
	mu     sync.RWMutex

	maxMessagesPerSecond int
	warningThreshold     int // 警告阈值
}

type messageRate struct {
	count     int
	lastReset time.Time
	warnings  int // 警告次数
}

// NewMessageRateLimiter 创建消息速率限制器
func NewMessageRateLimiter(maxPerSecond int) *MessageRateLimiter {
	return &MessageRateLimiter{
		limits:               make(map[string]*messageRate),
		maxMessagesPerSecond: maxPerSecond,
		warningThreshold:     maxPerSecond / 2,
	}
}

// AllowMessage 检查是否允许发送消息
func (ml *MessageRateLimiter) AllowMessage(clientID string) (allowed, warning bool) {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	now := time.Now()
	rate, exists := ml.limits[clientID]

	if !exists {
		ml.limits[clientID] = &messageRate{
			count:     1,
			lastReset: now,
		}
		return true, false
	}

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
	}
}

// AllowChat 检查是否允许发送聊天消息
func (cl *ChatRateLimiter) AllowChat(clientID string) (allowed bool, reason string) {
	cl.mu.Lock()
	defer cl.mu.Unlock()

	now := time.Now()
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

// ClearRateLimit 清除速率限制
func (cl *ChatRateLimiter) ClearRateLimit(clientID string) {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	delete(cl.limits, clientID)
}

package oci

import (
	"errors"
	"fmt"
	"io/ioutil"
	"math/rand"
	"oci-help/internal/applog"
	"oci-help/internal/config"
	"oci-help/internal/proxy"
	"oci-help/internal/store"
	"sync"
	"time"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/core"
	"github.com/oracle/oci-go-sdk/v65/identity"
	"github.com/oracle/oci-go-sdk/v65/limits"
	"github.com/oracle/oci-go-sdk/v65/monitoring"
	"github.com/oracle/oci-go-sdk/v65/networkloadbalancer"
	"github.com/oracle/oci-go-sdk/v65/ospgateway"
)

// adCache 缓存可用性域。用指针持有锁，保证 AccountCtx 被复制（如创建实例任务）时共享同一份缓存，
// 同时避免复制带锁的结构体。
type adCache struct {
	mu   sync.Mutex
	list []identity.AvailabilityDomain
}

// AccountCtx 承载单个甲骨文账号的凭证、SDK 客户端与运行时状态。
// 每个账号一个实例，CLI 与 Web 共用同一套操作；不同账号之间互不干扰，可并发调用。
type AccountCtx struct {
	Tenant *store.Tenant
	Name   string
	Oracle config.Oracle

	provider       common.ConfigurationProvider
	computeClient  core.ComputeClient
	networkClient  core.VirtualNetworkClient
	storageClient  core.BlockstorageClient
	identityClient identity.IdentityClient
	nlbClient      networkloadbalancer.NetworkLoadBalancerClient
	monitorClient  monitoring.MonitoringClient
	limitsClient   limits.LimitsClient
	ospClient      ospgateway.SubscriptionServiceClient
	// ads 指向可用性域缓存。用指针持有锁，保证 AccountCtx 被复制（如创建实例任务）时
	// 共享同一份缓存，同时避免复制带锁的结构体。
	ads *adCache

	// Logger 输出进度日志；为 nil 时回退到 CLI 的 printf。
	Logger func(format string, a ...interface{})
	// Progress 推送进度（stage/done/total）；为 nil 时忽略。
	Progress func(stage string, done, total int)
	// Stopped 返回 true 时中止批量创建；为 nil 时视为不中止。
	Stopped func() bool
}

// stopped 判断当前是否已被要求停止。
func (ac *AccountCtx) stopped() bool {
	return ac.Stopped != nil && ac.Stopped()
}

// sleepRandomSecond 在 min~max 秒之间随机等待，期间轮询停止请求；返回 false 表示被中止。
func (ac *AccountCtx) sleepRandomSecond(min, max int32) bool {
	return ac.sleepSeconds(randomSecond(min, max))
}

// sleepSeconds 等待指定秒数并打印日志，期间轮询停止请求；返回 false 表示被中止。
func (ac *AccountCtx) sleepSeconds(second int32) bool {
	if second < 0 {
		second = 0
	}
	ac.logf("Sleep %d Second...\n", second)
	deadline := time.Now().Add(time.Duration(second) * time.Second)
	for {
		if ac.stopped() {
			return false
		}
		remain := time.Until(deadline)
		if remain <= 0 {
			return true
		}
		if remain > 200*time.Millisecond {
			remain = 200 * time.Millisecond
		}
		time.Sleep(remain)
	}
}

// randomSecond 返回 [min,max] 闭区间内的随机秒数；参数非法时退化为 1 秒。
func randomSecond(min, max int32) int32 {
	if min <= 0 {
		min = 1
	}
	if max <= 0 || max < min {
		max = min
	}
	if min == max {
		return min
	}
	return rand.Int31n(max-min+1) + min
}

// newAccountCtx 根据数据库中的租户配置构造账号上下文并初始化各 SDK 客户端。
func NewAccountCtx(t *store.Tenant) (*AccountCtx, error) {
	if t == nil {
		return nil, errors.New("租户配置为空")
	}
	ac := &AccountCtx{Tenant: t, Name: t.Name, ads: &adCache{}}
	ac.Oracle = config.Oracle{
		User:         t.User,
		Fingerprint:  t.Fingerprint,
		Tenancy:      t.Tenancy,
		Region:       t.Region,
		Key_file:     t.KeyFile,
		Key_password: t.KeyPassword,
	}
	// 租户可配置独立代理，未配置时回退到全局代理。
	tenantProxy := t.Proxy
	if tenantProxy == "" {
		tenantProxy = config.C.Proxy
	}

	var err error
	if ac.provider, err = getProvider(ac.Oracle); err != nil {
		return nil, fmt.Errorf("获取 Provider 失败: %w", err)
	}
	if ac.computeClient, err = core.NewComputeClientWithConfigurationProvider(ac.provider); err != nil {
		return nil, fmt.Errorf("创建 ComputeClient 失败: %w", err)
	}
	proxy.Apply(&ac.computeClient.BaseClient, tenantProxy)
	if ac.networkClient, err = core.NewVirtualNetworkClientWithConfigurationProvider(ac.provider); err != nil {
		return nil, fmt.Errorf("创建 VirtualNetworkClient 失败: %w", err)
	}
	proxy.Apply(&ac.networkClient.BaseClient, tenantProxy)
	if ac.storageClient, err = core.NewBlockstorageClientWithConfigurationProvider(ac.provider); err != nil {
		return nil, fmt.Errorf("创建 BlockstorageClient 失败: %w", err)
	}
	proxy.Apply(&ac.storageClient.BaseClient, tenantProxy)
	if ac.identityClient, err = identity.NewIdentityClientWithConfigurationProvider(ac.provider); err != nil {
		return nil, fmt.Errorf("创建 IdentityClient 失败: %w", err)
	}
	proxy.Apply(&ac.identityClient.BaseClient, tenantProxy)
	if ac.nlbClient, err = networkloadbalancer.NewNetworkLoadBalancerClientWithConfigurationProvider(ac.provider); err != nil {
		return nil, fmt.Errorf("创建 NetworkLoadBalancerClient 失败: %w", err)
	}
	proxy.Apply(&ac.nlbClient.BaseClient, tenantProxy)
	if ac.monitorClient, err = monitoring.NewMonitoringClientWithConfigurationProvider(ac.provider); err != nil {
		return nil, fmt.Errorf("创建 MonitoringClient 失败: %w", err)
	}
	proxy.Apply(&ac.monitorClient.BaseClient, tenantProxy)
	if ac.limitsClient, err = limits.NewLimitsClientWithConfigurationProvider(ac.provider); err != nil {
		return nil, fmt.Errorf("创建 LimitsClient 失败: %w", err)
	}
	proxy.Apply(&ac.limitsClient.BaseClient, tenantProxy)
	if ac.ospClient, err = ospgateway.NewSubscriptionServiceClientWithConfigurationProvider(ac.provider); err != nil {
		return nil, fmt.Errorf("创建 SubscriptionServiceClient 失败: %w", err)
	}
	proxy.Apply(&ac.ospClient.BaseClient, tenantProxy)
	return ac, nil
}

// EnsureAvailabilityDomains 惰性加载可用性域，供引导卷等按域查询使用。
// 加锁保护：同一个 AccountCtx 会被多个 Web 请求/后台任务共享。
func (ac *AccountCtx) EnsureAvailabilityDomains() ([]identity.AvailabilityDomain, error) {
	if ac.ads == nil {
		return nil, errors.New("账号上下文未初始化")
	}
	ac.ads.mu.Lock()
	defer ac.ads.mu.Unlock()
	if len(ac.ads.list) > 0 {
		return ac.ads.list, nil
	}
	ads, err := ac.ListAvailabilityDomains()
	if err != nil {
		return nil, err
	}
	ac.ads.list = ads
	return ads, nil
}

// AvailabilityDomains 返回已缓存的可用性域（尚未加载时返回 nil）。
func (ac *AccountCtx) AvailabilityDomains() []identity.AvailabilityDomain {
	if ac.ads == nil {
		return nil
	}
	ac.ads.mu.Lock()
	defer ac.ads.mu.Unlock()
	return ac.ads.list
}

func (ac *AccountCtx) logf(format string, a ...interface{}) {
	if ac.Logger != nil {
		ac.Logger(format, a...)
		return
	}
	applog.Printf(format, a...)
}

func (ac *AccountCtx) progress(stage string, done, total int) {
	if ac.Progress != nil {
		ac.Progress(stage, done, total)
	}
}

func getProvider(oracle config.Oracle) (common.ConfigurationProvider, error) {
	content, err := ioutil.ReadFile(oracle.Key_file)
	if err != nil {
		return nil, err
	}
	privateKey := string(content)
	privateKeyPassphrase := common.String(oracle.Key_password)
	return common.NewRawConfigurationProvider(oracle.Tenancy, oracle.User, oracle.Region, oracle.Fingerprint, privateKey, privateKeyPassphrase), nil
}

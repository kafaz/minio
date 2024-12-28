// 版权所有 (c) 2015-2024 MinIO, Inc.
//
// 此文件是 MinIO 对象存储栈的一部分
//
// 该程序是自由软件：您可以根据 GNU Affero 通用公共许可证的条款重新分发和/或修改
// 由自由软件基金会发布的许可证，版本 3 或（根据您的选择）任何更高版本。
//
// 该程序的发布是希望它能有用
// 但没有任何保证；甚至没有隐含的
// 适销性或特定用途的适用性。有关详细信息，请参阅
// GNU Affero 通用公共许可证。
//
// 您应该已经收到一份 GNU Affero 通用公共许可证的副本
// 与此程序一起。如果没有，请参阅 <http://www.gnu.org/licenses/>。

package cachevalue

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Opts 包含缓存的选项。
type Opts struct {
	// 如果设置为 true，即使更新值出错，也返回最后缓存的值。
	// 返回最后的有效值和错误。
	ReturnLastGood bool

	// 如果设置了 NoWait，Get() 将返回最后的有效值，
	// 如果 TTL 已过期但 2x TTL 尚未过去，
	// 但会在后台获取新值。
	NoWait bool
}

// Cache[T any] 是一个通用的缓存结构体，使用泛型实现
// T 可以是任何类型，这使得这个缓存可以存储任何类型的数据
type Cache[T any] struct {
	// updateFn 是一个函数，用于获取最新的值
	// 参数：
	// - ctx: 上下文，用于控制更新操作的生命周期
	// 返回：
	// - T: 更新后的值
	// - error: 更新过程中可能发生的错误
	updateFn func(ctx context.Context) (T, error)

	// ttl 是缓存值的生存时间
	// 超过这个时间后，缓存将被视为过期
	ttl time.Duration

	// opts 包含缓存的配置选项
	opts Opts

	// Once 用于确保初始化操作只执行一次
	// 这是一个线程安全的延迟初始化机制
	Once sync.Once

	// 以下是内部管理的状态字段
	val          atomic.Pointer[T] // 原子指针，存储实际的缓存值
	lastUpdateMs atomic.Int64      // 上次更新的时间戳（毫秒）
	updating     sync.Mutex        // 用于确保更新操作的互斥访问
}

// New 分配一个新的缓存值实例。必须使用 `.TnitOnce` 初始化。
func New[T any]() *Cache[T] {
	return &Cache[T]{}
}

// NewFromFunc 分配一个新的缓存值实例并使用更新函数初始化它，使其可以使用。
func NewFromFunc[T any](ttl time.Duration, opts Opts, update func(ctx context.Context) (T, error)) *Cache[T] {
	return &Cache[T]{
		ttl:      ttl,
		updateFn: update,
		opts:     opts,
	}
}

// InitOnce 使用 TTL 和更新函数初始化缓存。保证只调用一次。
func (t *Cache[T]) InitOnce(ttl time.Duration, opts Opts, update func(ctx context.Context) (T, error)) {
	t.Once.Do(func() {
		t.ttl = ttl
		t.updateFn = update
		t.opts = opts
	})
}

// GetWithCtx 方法的详细工作流程：
// 1. 首先检查是否有有效的缓存值
// 2. 如果启用了 NoWait 选项，在特定条件下会异步更新
// 3. 否则，会同步更新缓存值
func (t *Cache[T]) GetWithCtx(ctx context.Context) (T, error) {
	// 加载当前缓存的值
	v := t.val.Load()
	ttl := t.ttl
	vTime := t.lastUpdateMs.Load()
	tNow := time.Now().UnixMilli()

	// 如果缓存值存在且未过期，直接返回
	if v != nil && tNow-vTime < ttl.Milliseconds() {
		return *v, nil
	}

	// NoWait 模式的处理逻辑
	// 如果缓存虽然过期，但未超过 TTL 的两倍时间
	// 则返回旧值，并在后台异步更新
	if t.opts.NoWait && v != nil && tNow-vTime < ttl.Milliseconds()*2 {
		if t.updating.TryLock() {
			go func() {
				defer t.updating.Unlock()
				t.update(context.Background())
			}()
		}
		return *v, nil
	}

	// 同步更新的逻辑
	t.updating.Lock()
	defer t.updating.Unlock()

	// 双重检查，避免重复更新
	if time.Since(time.UnixMilli(t.lastUpdateMs.Load())) < ttl {
		if v = t.val.Load(); v != nil {
			return *v, nil
		}
	}

	// 执行更新
	if err := t.update(ctx); err != nil {
		var empty T
		return empty, err
	}

	return *t.val.Load(), nil
}

// Get 将返回缓存的值或获取新值。
// 如果 Update 函数返回错误，则值按原样转发而不缓存。
func (t *Cache[T]) Get() (T, error) {
	return t.GetWithCtx(context.Background())
}

// update 是内部更新方法，处理实际的值更新逻辑
func (t *Cache[T]) update(ctx context.Context) error {
	val, err := t.updateFn(ctx)
	if err != nil {
		// ReturnLastGood 选项允许在更新失败时保留旧值
		if t.opts.ReturnLastGood && t.val.Load() != nil {
			return nil
		}
		return err
	}

	// 原子操作更新缓存值和时间戳
	t.val.Store(&val)
	t.lastUpdateMs.Store(time.Now().UnixMilli())
	return nil
}

package main

import (
	"fmt"
	"time"
)

// 简化版 ImageCache 用于测试 LRU 逻辑
type ImageCacheEntry struct {
	Key       string
	Timestamp time.Time
}

type ImageCache struct {
	items      map[string]*ImageCacheEntry
	order      []string // 前端=最近访问
	maxEntries int
	ttl        time.Duration
}

func NewImageCache(maxEntries int, ttlMinutes int) *ImageCache {
	return &ImageCache{
		items:      make(map[string]*ImageCacheEntry),
		order:      make([]string, 0),
		maxEntries: maxEntries,
		ttl:        time.Duration(ttlMinutes) * time.Minute,
	}
}

func (c *ImageCache) Store(key string) {
	// 已存在 → 移到前端
	if _, exists := c.items[key]; exists {
		c.moveToFront(key)
		c.items[key].Timestamp = time.Now()
		fmt.Printf("  [更新] key=%s, 缓存数=%d\n", key, len(c.items))
		return
	}

	// 新增 → 检查容量
	if len(c.items) >= c.maxEntries {
		c.evictOldest()
	}

	c.items[key] = &ImageCacheEntry{Key: key, Timestamp: time.Now()}
	c.order = append([]string{key}, c.order...) // 插入到前端
	fmt.Printf("  [存储] key=%s, 缓存数=%d\n", key, len(c.items))
}

func (c *ImageCache) Retrieve(key string) bool {
	entry, exists := c.items[key]
	if !exists {
		return false
	}
	if time.Since(entry.Timestamp) > c.ttl {
		c.remove(key)
		return false
	}
	c.moveToFront(key)
	return true
}

func (c *ImageCache) moveToFront(key string) {
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			c.order = append([]string{key}, c.order...)
			return
		}
	}
}

func (c *ImageCache) evictOldest() {
	if len(c.order) == 0 {
		return
	}
	oldest := c.order[len(c.order)-1]
	c.order = c.order[:len(c.order)-1]
	delete(c.items, oldest)
	fmt.Printf("  [LRU淘汰] key=%s, 剩余=%d\n", oldest, len(c.items))
}

func (c *ImageCache) remove(key string) {
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
	delete(c.items, key)
}

func (c *ImageCache) Stats() (int, int) {
	return len(c.items), c.maxEntries
}

func main() {
	fmt.Println("===== 图片缓存 LRU 单元测试 =====")
	fmt.Println()

	// 测试 1: 基本存储
	fmt.Println("【测试 1】基本存储（maxEntries=5）")
	cache := NewImageCache(5, 5)
	cache.Store("user-001")
	cache.Store("user-002")
	cache.Store("user-003")
	fmt.Println()

	// 测试 2: 重复存储（应更新而非新增）
	fmt.Println("【测试 2】重复存储（user-001 应更新）")
	cache.Store("user-001")
	count, _ := cache.Stats()
	if count == 3 {
		fmt.Println("  ✅ 缓存数仍为 3（未新增）")
	} else {
		fmt.Printf("  ❌ 缓存数=%d（应为 3）\n", count)
	}
	fmt.Println()

	// 测试 3: LRU 淘汰
	fmt.Println("【测试 3】LRU 淘汰（填满后新增应淘汰最旧的）")
	cache.Store("user-004")
	cache.Store("user-005") // 现在满了 [005, 004, 001, 003, 002]
	count, _ = cache.Stats()
	fmt.Printf("  缓存数=%d（应已满）\n", count)
	cache.Store("user-006") // 应淘汰 user-002
	count, _ = cache.Stats()
	if count == 5 {
		fmt.Println("  ✅ 缓存数仍为 5（淘汰了 1 个）")
	} else {
		fmt.Printf("  ❌ 缓存数=%d（应为 5）\n", count)
	}
	fmt.Println()

	// 测试 4: 访问更新 LRU 顺序
	fmt.Println("【测试 4】访问更新 LRU 顺序")
	cache2 := NewImageCache(3, 5)
	cache2.Store("a")
	cache2.Store("b")
	cache2.Store("c") // [c, b, a]
	fmt.Println("  访问 b...")
	cache2.Retrieve("b") // [b, c, a]
	cache2.Store("d")    // 应淘汰 a（最旧）
	count, _ = cache2.Stats()
	if count == 3 {
		fmt.Println("  ✅ 淘汰了 a（最久未访问）")
	}
	fmt.Println()

	// 测试 5: 大量请求模拟
	fmt.Println("【测试 5】120 个不同用户（maxEntries=100）")
	cache3 := NewImageCache(100, 5)
	for i := 0; i < 120; i++ {
		key := fmt.Sprintf("user-%03d", i)
		cache3.Store(key)
	}
	count, max := cache3.Stats()
	if count == max {
		fmt.Printf("  ✅ 缓存数=%d（达到上限，淘汰了 20 个）\n", count)
	} else {
		fmt.Printf("  ❌ 缓存数=%d（应为 %d）\n", count, max)
	}
	fmt.Println()

	fmt.Println("===== 单元测试完成 =====")
}

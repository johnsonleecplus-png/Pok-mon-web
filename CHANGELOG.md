# MARS-宝可梦卡牌库 · 变更日志

## 2026-07-28 — 代码健康度修复 (P0-P2)

### 🔴 P0 修复

**搜索功能实现** — 之前搜索框 UI 完整但后端完全不处理 `q` 参数
- `queryCards()` 新增 `q string` 参数，搜索 `card_name` / `card_number` / `attacks` 三个字段
- `handleCardsPartial` 读取 `?q=` 并传递
- 有搜索时禁用 interleaved 模式（保证结果按相关性展示）
- 所有 `queryCards()` 调用方更新（`handleHome`、`handleCardDetail`）

**`calcBuildStamp()` 执行顺序修复**
- `main()` 中将 `wd` 和 `staticDir` 的计算移到 `syncParentCSS()` 和 `calcBuildStamp()` 之前
- 现在 buildStamp 正确反映静态文件 mtime

### 🟠 P1 修复

**合并重复的 `htmx:afterSwap` 监听器**
- `layout.html` 原来有 2 个独立的 `htmx:afterSwap`（第 343 行 + 第 367 行）
- 合并为 1 个 handler，统一处理 card-grid 和 card-detail-modal
- 删除 `card_detail.html` 中重复的内联 fav/rating/notes 恢复脚本（现在由合并后的 handler 统一做）

**F5/Ctrl+R 拦截修复**
- 原来按 F5 会跳回封面页（`window.location.href = '/'`）
- 改为 `window.location.reload(true)`，保留在 `/app` 页面刷新

**`typeENMap` 提取为全局变量**
- `funcMap.typeName` 和 `queryCards()` 之前各有一份相同的 ZH→EN 映射
- 提取为全局 `var typeENMap`，两处引用同一数据，消除 drift 风险

**`refresh()` 中 `price_min` 重复行** — 删除第 398 行死代码

**`cover.html` 注释乱码** — "绫崇櫢" → "米白"

### 🟡 P2 清理

**删除 `var _ = filepath.Join` 死代码** — `filepath` 已在多处使用，该行多余

**删除 CSS `.modal:target` 选择器** — 从未被使用（实际靠 `.show` class）

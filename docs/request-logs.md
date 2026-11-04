# 请求日志与结算异常

普通用户查看自己的日志；管理员查看全部。按请求 ID、模型 ID、HTTP 状态及时间范围筛选。默认每页 20 项，可选择 10/20/50/100；显示总数及当前页，支持前后翻页。筛选和每页条数变更会回到第一页，翻页与结算处理保留筛选。列表按时间、请求 ID 降序稳定排序；持续有新请求时可刷新更新列表。

## 结算异常

独立“待核算请求”页面与侧栏项已删除。管理员在请求日志选择“结算异常”后点击筛选：

- 详情：请求、用户、模型、原预占、状态、异常原因和价格快照。
- 重试结算：仅重试账本操作，不会重复调用上游。有可靠用量按原价快照结算，否则按 0 释放预占，操作前会确认。
- 人工核算：填写明确的 USD 金额和 3–200 字节依据；释放预占明确填 0，不接受省略/空金额。原请求窗口、账本、日志和审计原子提交。

只有 pending 记录允许重试/人工处理，在途记录不能人工结算。重试成功记录审计，重复重试已结算记录不会重复扣款；价格快照损坏或数据库异常则保留异常。即使当时日志写入失败，异常也可从账本显示；HTTP 状态显示“未记录”，不伪造成功/失败状态。恢复后不重复显示同一请求。

普通用户没有异常筛选和管理按钮，也不能调用管理员详情/结算 API；仍可看到自身日志的结算状态和原因。已删除的旧 `#charges` 书签跳转到日志页，管理员自动选择异常筛选。

## API

- `GET /api/logs?page=1&page_size=20`：自己的日志。
- `GET /api/admin/logs?page=1&page_size=20&settlement=pending`：管理员结算异常。
- 可选 `model`、`request_id`、`status`、`since`；管理员还可选择 `settlement=pending/settled/inflight`。
- 分页返回 `{items,total,page,page_size}`。page 从 1 开始、最大 1000000，page_size 为 1–100，省略时分别默认 1/20。总数和页内容在同一读取事务内查询；翻页间新增记录可能改变后续页位置。
- 不传分页/结算筛选参数时保留旧数组接口（最多 200 条），旧 `before/before_id` 游标接口继续可用。
- `GET /api/admin/logs/{id}/settlement`：详情。
- `POST /api/admin/logs/{id}/retry-settlement`：重试，不产生新的推理请求。
- `POST /api/admin/logs/{id}/settle`：`{amount,reason}`，amount 为 microUSD；须管理员会话及 CSRF。
- 旧 `/api/admin/charges`、`/api/admin/charges/{id}/settle` 兼容保留。

日志不保存提示词、完整响应、上游 Key 或 SMTP 密码。费用策略见 [自动结算](automatic-settlement.md)。

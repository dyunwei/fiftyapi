# 模型协议与原生转发

客户端使用哪个标准协议，网关只选择支持该协议的模型来源。协议不匹配返回 400，不调用上游、不预占预算。已删除 Chat→Messages 转换器，不会把 Chat 改成 Messages/Responses，也不会将其他协议的响应伪装为 Chat。

| 来源 | 提供的标准协议 |
| --- | --- |
| Claude OAuth | Messages |
| OpenAI Codex OAuth | Responses（HTTP/SSE 与已支持的原生 WebSocket） |
| Antigravity OAuth | 专用适配提供 Messages；内部仍转换为 Antigravity 请求，不提供 Chat 自动转换 |
| API Key 上游（New API/Sub2Api 等） | 管理员声明的 Chat Completions、Messages、Responses，可以同时支持多种 |

## 如何配置上游 API

1. “上游账号 → +接入上游API”，填写地址和 Key。
2. 勾选上游真实支持的默认协议，新账号默认只选择 Chat Completions，其他协议按实际能力添加。
3. 自动读取模型目录，仅自动发现 ID，不据此猜测协议权限。
4. 在“模型发现与映射”中可以覆盖具体模型的协议。每行可多选，例如同一模型勾选 Chat 和 Responses。覆盖会替换该模型的默认协议，不是继续叠加默认值。
5. 同步后在账号“查看模型”和“模型与价格”查看完整支持协议，再设置价格。一个多协议模型共用一个 ID 和一份价格。接口能力依然以实际调用为准。

例如 models 中一行配置为：

```json
{"id":"my-model","upstream_id":"upstream-model","protocols":["chat","responses"]}
```

该 ID 可以用于 /v1/chat/completions 和 /v1/responses，不支持 /v1/messages。同一个上游 API 还可以提供其他模型，各模型的协议组合独立。协议更改在验证同步成功后更新；修改账号配置到同步完成前不会继续路由该账号。

协议由来源能力决定，价格编辑窗口只展示协议并编辑金额，不能单独勾选一个上游实际没有的协议。多协议模型共用内部单价；如果不同接口需要不同的内部单价，可以为互不重叠的协议配置不同别名与价格。

## 原生 Chat 的保留与核算

Chat 直通 /v1/chat/completions。仅替换模型为配置的上游 ID；system/developer、tools、tool_choice、response_format、reasoning 等字段由上游验证，不再经过 Messages 转换器筛选或改写。user/metadata 也会原样发送给所选上游，应按客户端需求设置。认证使用保存的上游 Key，不转发用户本地 Key。

流式请求设置 stream_options.include_usage=true，以便接收可靠用量，保留其他 stream_options 字段。响应的 Chat JSON/SSE、工具参数、推理及扩展字段保留；账本仅提取 prompt_tokens/completion_tokens/cached_tokens，缓存输入不重复计费。多个候选 n=1–128，预占考虑候选数量；仍仅支持标准 auto/default 服务档位。

要求上游按 stream 选择 JSON 或 SSE。不聚合 SSE 为 Chat JSON，也不把 JSON 改写成 Chat SSE。流式必须收到 [DONE]，截断不补成功标记；已观测可靠 usage 仍结算，缺失/无效 usage 依既有规则按 0 释放预占并标明原因，不代表上游免费。一次逻辑请求沿用统一调度、队列、失败切换、日志和结算，不另开账本。

/v1/models 返回 protocols、supported_endpoints 和 supported_endpoint_types。部分客户端不会读取扩展字段，仍需选择正确的供应商/接口模式；不能根据模型名称推断。Chat 模型只声明 openai，Messages 只声明 anthropic，Responses 只声明 openai-response；多协议模型同时声明实际配置的能力。

## 兼容和升级

schema 9 只新增模型协议元数据，保留旧 ID、价格和历史。旧配置的 protocol 标量仍可读取；新表单保存 protocols 数组。启用多协议同步优先复用已有 ID/价格，并保留仍启用的旧协议专属 ID 作为兼容记录，不自动合并原本不同的价格。ID 文本可能仍含旧协议名称，以目录 protocols 为准。

移除协议后，对应接口立即不再可路由。针对某协议的模型冷却不阻断其他协议；账号级冷却仍影响全部协议，旧模型级冷却保守兼容。编辑协议后重新同步，客户端重新获取目录。

原来通过 Chat 调用 Claude、Antigravity 或只支持 Messages 的 API 模型，现在会被拒绝，需要在客户端改为 Anthropic Messages 模式。Codex OAuth 继续使用 Responses。没有自动选择其他模型或协议作为降级。

升级前备份数据库和原主密钥，新程序启动自动执行迁移 009；回退需恢复升级前备份，不能用旧程序打开 schema 9。说明见 [部署文档](deployment.md)。

## 验证记录

2026-11-03：全量 Go race 和 vet、4 项表单检查、最终二进制 14 项浏览器测试通过。覆盖同一 New API 模型 ID 的 Chat/Responses 正确接口与共享价格、协议移除、旧 ID/价格保留、互不重叠别名反复同步、Chat JSON/SSE/缓存用量/截断、模型协议冷却隔离。真实运行库的只读快照 schema 8→9 迁移通过，账号密文、模型、用户与 Key 保留；没有启动账号维护或请求真实上游。第三方真实站点仍需按实际账号验证。


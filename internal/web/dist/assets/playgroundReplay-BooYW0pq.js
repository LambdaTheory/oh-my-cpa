import{w as u,r as g,R as m,c as i,a as h,d as $}from"./pacing-7E6q0Kto.js";const f=`**Explaining prompt caching**

The user wants a concrete example. I'll pick a long shared prefix (a system prompt plus tool definitions), show the first request writing the cache and the later ones reading it, then put numbers on it with illustrative prices so the saving is visible. I should also say what makes a cache hit, since that is the part people get wrong.`,w={zh:`提示词缓存让模型提供方把请求开头**重复出现的部分**（系统提示词、工具定义、长文档）保存下来。后续请求只要开头一致，这部分就按缓存读取计价，而不是按完整输入计价。

**一个例子**

假设一个编码助手每次请求都带着 20,000 Token 的系统提示词与工具定义，再加上约 1,000 Token 的新问题，一天发送 100 次。下面用示意价格计算：普通输入 $3 / 百万 Token，缓存写入 $3.75，缓存读取 $0.30。

| | 不使用缓存 | 使用缓存 |
| --- | --- | --- |
| 第 1 次请求 | 21,000 × $3 = $0.063 | 20,000 × $3.75 + 1,000 × $3 = $0.078 |
| 之后每次请求 | $0.063 | 20,000 × $0.30 + 1,000 × $3 = $0.009 |
| 100 次合计 | **$6.30** | **$0.97** |

第一次请求因为要写入缓存略贵一些，之后每次便宜约 86%，一天下来输入成本下降约 85%。

**需要注意**

- 只有**前缀完全一致**才会命中：把稳定的内容放在最前，变化的内容放在最后。
- 缓存有有效期，长时间没有请求会失效并重新写入。
- 价格与规则因提供方而异。在 Oh My CPA 的仪表盘里，「缓存率」与「缓存读」就是用来观察命中情况的。`,en:`Prompt caching lets a model provider keep the **repeated opening** of a request (a system prompt, tool definitions, a long document). Later requests that start the same way are billed for that part at the cache-read rate instead of the full input rate.

**An example**

Say a coding assistant sends a 20,000-token system prompt and tool definitions with every request, plus about 1,000 tokens of new question, 100 times a day. With illustrative prices of $3 per million input tokens, $3.75 to write the cache and $0.30 to read it:

| | Without caching | With caching |
| --- | --- | --- |
| First request | 21,000 × $3 = $0.063 | 20,000 × $3.75 + 1,000 × $3 = $0.078 |
| Each later request | $0.063 | 20,000 × $0.30 + 1,000 × $3 = $0.009 |
| 100 requests | **$6.30** | **$0.97** |

The first request costs slightly more because it writes the cache; every later one is about 86% cheaper, and the day's input cost falls by about 85%.

**Worth knowing**

- Only an **identical prefix** hits the cache: put what is stable first and what changes last.
- A cache entry expires, so a long gap between requests writes it again.
- Prices and rules differ by provider. On the Oh My CPA dashboard, "Cache rate" and "Cache read" are there to watch how often it hits.`},y={prompt_tokens:27,completion_tokens:486,total_tokens:513},o={thought:f,reply:w,usage:y},k=14;async function b(c,n,l,p,a=u){const t=e=>{l(e)},r=g(p),s=Date.now();t({type:"meta",model:c.model,started_at_ms:s}),await a(m,n);for(const e of i(o.thought,k))t({type:"thought",content:e}),await a(h,n);const d=Date.now()-s;for(const e of i(o.reply[r],$(r)))t({type:"delta",content:e}),await a(h,n);t({type:"usage",usage:o.usage}),t({type:"done",finish_reason:"stop",duration_ms:Date.now()-s,first_content_ms:d})}export{b as replayPlaygroundChat};

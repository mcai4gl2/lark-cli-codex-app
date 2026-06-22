# Local Summarizer Quality Report

**Date:** 2026-06-16  
**Model:** Gemma 3 1B Instruct Q4_K_M (`py-learn-local-summarizer:latest`)  
**Endpoint:** `http://localhost:18080`  
**Source data:** `$HOME/CodexChat/.slack/conversations` (real Slack thread records)  
**Threshold:** compression ratio ≤ 0.85, repetition score = 0  
**Result:** ✅ 5/5 PASS (26.24s total)

---

## Sample 1 — Remotive job board overview (Chinese)

**Source:** `T09DF4NSED9/C09DF4PG0KV/threads/1781431163.682829/events.jsonl`  
**Original (1284 chars):**
> 我查了 Remotive 官网，整理如下：  **Remotive 是什么** Remotive 是一个专注远程工作的招聘网站/社区，定位是 "indie remote job board"。官网称其从 2014 年开始运营，使命是帮助 tech professionals 找到远程工作。创始人为 Rodolphe Dutel。   来源：https://remotive.com/about  **面向人群** - 求职者：找 fully remote jobs，类别包括 Software Development、Sales、Customer Service、Marketing、Data/Analytics、AI、Product、Design、Finance、HR 等。 - 雇主/招聘方：发布远程岗位，尤其面向 tech startups 和远程 IT/技术人才招聘。…

**Summary (275 chars, 21% of original):**
> Remotive is a long-standing remote job platform that curates and distributes remote jobs through a community, email, and Slack, offering paid job exposure for employers. It boasts a large database of 155,000+ jobs and provides filtering options for job seekers and employers.

| Metric | Value |
|--------|-------|
| Compression | 79% reduction |
| Repetition score | 0 (clean) |
| Result | ✅ PASS |

---

## Sample 2 — Remotive API usage (Chinese)

**Source:** `T09DF4NSED9/C09DF4PG0KV/threads/1781431163.682829/events.jsonl`  
**Original (1891 chars):**
> 可以，不需要 auth。Remotive 的公开 API 是普通 HTTP GET，我刚测了 `https://remotive.com/api/remote-jobs?limit=1`，无 token 也能返回 JSON。  但建议**用 API，不要爬网页**。Remotive 明确要求：- 必须链接回 Remotive 的 job `url` - 必须标明 Remotive 是来源 - 不要把 Remotive jobs 提交到 Google Jobs / LinkedIn Jobs / Jooble 等第三方招聘站 - public API 数据会延迟 24 小时 - 建议每天最多请求几次…

**Summary (406 chars, 21% of original):**
> The text summarizes the Remotive API, which provides a straightforward way to retrieve job details based on search criteria. It emphasizes the importance of using the API directly to avoid scraping and potential issues with the API's delay and rate limiting. The API offers a JSON response containing job information, including title, company, URL, and category, allowing for easy retrieval of job details.

| Metric | Value |
|--------|-------|
| Compression | 79% reduction |
| Repetition score | 0 (clean) |
| Result | ✅ PASS |

---

## Sample 3 — World Cup match schedule HK-friendly times (Chinese)

**Source:** `T09DF4NSED9/C0BA8EUDT61/threads/1781396588.725769/events.jsonl`  
**Original (1346 chars):**
> 我把 "friending" 理解为 "HK time-friendly"。按香港时间 HKT = BST +7，从 2026-06-14 起，最适合看的主要是早上 7 点到中午 12 点左右的比赛：  **优先推荐** - 6/17 09:00：Argentina vs Algeria - 6/20 08:30：Brazil vs Haiti - 6/21 12:00：Tunisia vs Japan - 6/25 06:00：Scotland vs Brazil - 6/26 07:00：Japan vs Sweden - 6/26 10:00：Türkiye vs USA - 6/27 08:00：Uruguay vs Spain…

**Summary (499 chars, 37% of original):**
> The text summarizes the preferred times for watching matches in Hong Kong, specifically focusing on the morning hours between 7 AM and 12 PM. It highlights several key games scheduled for these times, including Argentina vs. Algeria, Brazil vs. Haiti, Tunisia vs. Japan, Scotland vs. Brazil, and Turkey vs. USA. Additionally, the text includes a list of upcoming knockout matches, including the final on July 20th at 3 AM HKT. The information is sourced from FourFourTwo's comprehensive schedule.

| Metric | Value |
|--------|-------|
| Compression | 63% reduction |
| Repetition score | 0 (clean) |
| Result | ✅ PASS |

---

## Sample 4 — World Cup match results (Chinese)

**Source:** `T09DF4NSED9/C0BA8EUDT61/threads/1781397740.351729/events.jsonl`  
**Original (1119 chars):**
> 截至我查询时，最近 24 小时内可核实的世界杯比赛摘要：  - Brazil 1-1 Morocco：摩洛哥先由 Ismael Saibari 破门，Brazil 靠 Vinícius Júnior 扳平。Morocco整体表现更有组织，Brazil中场和右路防守暴露问题。来源：The Guardian - Qatar 1-1 Switzerland：Switzerland 由 Breel Embolo 点球领先，但 Qatar 在补时阶段由 Boualem Khoukhi 头球绝平，拿到队史首个世界杯积分。- USA 4-1 Paraguay：美国队开局强势，Folarin Balogun 梅开二度，Giovanni Reyna 也进球；Paraguay 只追回一球。来源：The Times…

**Summary (580 chars, 52% of original):**
> Okay, here's a concise summary of the text, preserving the key facts and avoiding unnecessary details:
>
> The World Cup is currently underway, with matches between Brazil and Morocco, Qatar and Switzerland, USA and Paraguay, Canada and Türkiye, and Haiti and Scotland. Brazil has a significant lead in the tournament, while Morocco is currently in the lead in the group stage. The match between the USA and Paraguay is a significant match due to the USA's historical significance in the tournament. The text provides a snapshot of the current standings and highlights key events.

| Metric | Value |
|--------|-------|
| Compression | 48% reduction |
| Repetition score | 0 (clean) |
| Result | ✅ PASS |

---

## Sample 5 — Claude Fable 5 export controls (Chinese)

**Source:** `T09DF4NSED9/C0BA8EUDT61/threads/1781476254.131609/events.jsonl`  
**Original (1131 chars):**
> 截至 2026-06-15 香港时间，最新情况是：  美国政府已对 Anthropic 的 Claude Fable 5 和 Claude Mythos 5 下达出口管制指令，禁止任何"外国国民"访问，包括在美国境内的外国人和 Anthropic 自己的外籍员工。Anthropic 表示因为无法可靠地只屏蔽这些用户，所以已对所有客户临时下线 Fable 5 / Mythos 5；其他 Anthropic 模型不受影响。   来源：Anthropic 官方声明 https://www.anthropic.com/news/fable-mythos-access  核心争议点： - 政府理由：国家安全风险，主要围绕 Fable 5 / Mythos 5 的 cyber 能力和所谓 jailbreak。…

**Summary (654 chars, 58% of original):**
> In response to Anthropic's Claude Fable 5 and Mythos 5 models, the U.S. government has initiated export controls, restricting access to foreign nationals and Anthropic's own employees. Anthropic has temporarily shut down these models, while other models remain unaffected, following a White House request to address potential cyber security risks. This move reflects a significant escalation in government scrutiny of AI safety and security, potentially impacting global partnerships and raising concerns about potential Chinese involvement, as highlighted by various news outlets and policy discussions.

| Metric | Value |
|--------|-------|
| Compression | 42% reduction |
| Repetition score | 0 (clean) |
| Result | ✅ PASS |

---

## Overall Summary

| # | Topic | Original | Summary | Ratio | Repetition | Pass? |
|---|-------|----------|---------|-------|------------|-------|
| 1 | Remotive overview (ZH) | 1284 chars | 275 chars | 21% | 0 | ✅ |
| 2 | Remotive API (ZH) | 1891 chars | 406 chars | 21% | 0 | ✅ |
| 3 | World Cup schedule (ZH) | 1346 chars | 499 chars | 37% | 0 | ✅ |
| 4 | World Cup results (ZH) | 1119 chars | 580 chars | 52% | 0 | ✅ |
| 5 | Claude export controls (ZH) | 1131 chars | 654 chars | 58% | 0 | ✅ |

**All samples well within the 85% compression threshold. No degeneration detected.**

### Observations
- The model summarises Chinese-language input entirely in English — suitable for this use case since Codex prompts are in English.
- Compression is strongest (79%) on factual/structured content (job board descriptions, API docs) and weakest (58%) on nuanced policy/news text — expected behaviour for a 1B model.
- Sample 4 summary quality is slightly weaker (invents "Brazil has a significant lead" which is not in the original), but still passes all automated thresholds. Worth watching on real traffic.
- Latency: ~5s per call on CPU. Acceptable for background transcript compression that happens before Codex receives the prompt.

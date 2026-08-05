# 🤖 O.L.L.I. (Ollama-based Local LLM Interface)

**O.L.L.I.**는 로컬 [Ollama](https://ollama.com) 모델과 연동하여 동작하는 **Go 언어 기반 자율 AI 에이전트 인터페이스**입니다.

---

## ✨ 핵심 기능 (Key Features)

- **🕵️ Dedicated Subagent Delegation**:
  - **`delegate_researcher`**: 웹 검색(`web_search`) 및 URL 읽기(`read_url_content`) 전담 **Web Researcher Subagent**
  - **`delegate_coder`**: 코드 탐색(`grep_search`), 파일 보기(`view_file`), 파일 편집(`edit_file`) 전담 **Coder Subagent**
  - **📄 1-Turn JSONL File Handover**: 서브에이전트 작업 수행 과정 및 결과는 `./sessions/subagents/subagent_<id>.jsonl` 파일로 저장되며 메인 에이전트에는 핵심 보고서만 전달되어 토큰 및 대화 오염 완전 방지!
- **🛡️ 3-Mode Tool Permission**: `auto`, `ask`, `accept-edit` (동적 `config.json` 화이트리스트 및 `[a] Always` 영구 등록)
- **🔒 Multi-Layer Security Boundary**: 홈 디렉토리(`~`), 시스템 루트(`/`), 워크스페이스 외곽 이탈(`..`) 완전 방어
- **🎯 Goal Steering & Autonomous Loop**: 매 턴마다 활성 목표(Goal)를 지속 주입하여 Goal Drift 없이 임무 달성
- **💾 JSONL Session Persistence & RAG Search**: 대화 내역 자동 지속화, 세션 로드/리네임, 과거 로그 RAG 검색
- **🌐 Environment & Temporal Context**: 타임존 포함 실시간 시공간 맥락 및 디렉토리 위치 자동 인지
- **⌨️ `ergochat/readline` CJK UTF-8 REPL**: 한글 백스페이스 완벽 지원, 방향키 히스토리, Tab 커맨드 자동 완성
- **🌊 Unified Real-time Streaming**: Thinking(추론 과정) ➡️ Tool Call ➡️ Response 실시간 스트리밍

---

## 🚀 빌드 및 실행

```bash
./build.sh
./bin/olli
```

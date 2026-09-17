# O.L.L.I. Presenter 사용 가이드

O.L.L.I. Presenter는 발표 내용을 구조화하고, 검증된 템플릿 Renderer로 독립 실행형 HTML 슬라이드를 만드는 서브에이전트입니다. 모델이 HTML·CSS·JavaScript 전체를 직접 작성하지 않기 때문에 작은 로컬 모델에서도 디자인 일관성, 반응형 동작, 접근성, 생성 속도를 안정적으로 유지합니다.

## 빠른 시작

O.L.L.I.에게 발표 목적, 청중, 근거 자료, 원하는 결과를 자연어로 요청합니다.

```text
새 개발팀 루프 엔진을 엔지니어들에게 소개하는 발표 자료를 만들어줘.
Architect, Cassandra, Detail Planner, Coder, Reviewer, Verifier 흐름과
Counter/Tetris 검증 결과를 포함하고 technical-editorial 템플릿을 사용해줘.
```

메인 에이전트는 내부적으로 다음 도구를 호출합니다.

```json
{
  "task_description": "새 개발팀 루프 엔진을 엔지니어들에게 소개한다. Architect, Cassandra, Detail Planner, Coder, Reviewer, Verifier 흐름과 Counter/Tetris 검증 결과를 포함한다.",
  "template": "technical-editorial"
}
```

`template`을 생략하거나 `auto`로 지정하면 Presenter가 청중과 내용에 맞는 시안을 선택합니다.

성공하면 현재 워크스페이스에 `.html` 파일이 생성되고, Presenter 보고서의 `Artifact Files`에 경로가 표시됩니다. 파일은 외부 라이브러리 없이 브라우저에서 직접 열 수 있는 단일 HTML입니다.

## 템플릿 선택

| 템플릿 | 적합한 발표 | 주요 레이아웃 |
|---|---|---|
| `technical-editorial` | 아키텍처, 코드, 운영 흐름, 검증 결과 | hero, pipeline, cards, code, evidence |
| `product-narrative` | 문제 → 해결책 → 기능 → 증거로 이어지는 제품 소개 | problem, contrast, pipeline, cards, evidence |
| `executive-brief` | 의사결정, 지표, 위험, 후속 조치를 짧게 전달하는 보고 | statement, metrics, cards, evidence |
| `minimal-keynote` | 한 장에 한 메시지를 크게 보여 주는 짧은 키노트 | hero, statement, contrast, metrics |
| `auto` | Presenter가 목적과 청중에 따라 자동 선택 | 선택된 템플릿에 따름 |

### 기술 설계 리뷰

```text
결제 서비스 마이그레이션 설계를 8장 이내의 기술 발표로 만들어줘.
의존성 흐름, 실패 모드, 롤백 전략, 검증 계획을 포함하고
technical-editorial 템플릿을 사용해줘.
```

### 제품 제안

```text
신규 비용 관리 기능을 경영진에게 제안하는 발표를 만들어줘.
사용자 문제, 해결 동선, 핵심 기능, 기대 효과, 다음 결정을 포함하고
product-narrative 템플릿을 사용해줘.
```

### 경영 요약

```text
분기 안정성 개선 결과를 6장 이내로 요약해줘.
확인된 수치만 사용하고 위험과 다음 결정을 분명히 보여줘.
executive-brief 템플릿을 사용해줘.
```

### 짧은 키노트

```text
'제약이 자율성을 확장한다'는 메시지로 5장 키노트를 만들어줘.
한 장에 한 메시지만 쓰고 minimal-keynote 템플릿을 사용해줘.
```

## 좋은 요청 작성법

Presenter는 제공된 사실을 재구성하지만 없는 수치나 증거를 만들어서는 안 됩니다. 요청에 다음 네 요소를 넣으면 결과가 좋아집니다.

1. **청중**: 엔지니어, 고객, 경영진, 신규 사용자 등
2. **목적**: 설명, 승인 요청, 설계 리뷰, 회고, 교육 등
3. **검증된 사실**: 수치, 테스트 결과, 제품 동작, 결정된 제약
4. **제외할 내용**: 추정치, 미승인 로드맵, 외부 자료 등

예:

```text
청중은 Go 백엔드 엔지니어다. 목적은 새 개발팀 파이프라인의 설계 리뷰다.
확인된 사실은 전체 회귀 테스트와 vet 통과, Counter 및 Tetris 제품 스모크 통과다.
처리량 개선 수치는 측정하지 않았으므로 만들지 마라. 7장 이내로 구성해줘.
```

## 출력 동작

모든 템플릿은 다음 기능을 공통으로 제공합니다.

- `ArrowRight` 또는 `Space`: 다음 슬라이드
- `ArrowLeft`: 이전 슬라이드
- `Home`: 첫 슬라이드
- `End`: 마지막 슬라이드
- 화면 하단의 이전·다음 버튼
- 현재 슬라이드 카운터와 진행 막대
- 모바일 단일 열 레이아웃
- `prefers-reduced-motion` 대응
- 활성 슬라이드와 진행률의 ARIA 상태 갱신

모델이 작성한 제목과 본문은 Renderer에서 HTML escape되며, 외부 스크립트·스타일·네트워크 요청·폼·다운로드 요소는 생성하지 않습니다.

## 설정

### Presenter 모델

`config.json`의 `subagents.presenter`에서 Presenter 모델을 독립적으로 지정합니다.

```json
{
  "subagents": {
    "presenter": {
      "model": "gemma4:12b",
      "thinking": false
    }
  }
}
```

권장값은 `gemma4:12b`와 `thinking: false`입니다. Presenter는 긴 추론보다 슬라이드 구조와 시각적 그룹화가 중요하고, thinking을 끄면 로컬 생성 시간이 크게 줄어듭니다.

모델명이 비어 있으면 현재 메인 대화 모델로 fallback합니다. `thinking`을 생략하면 모델 또는 상위 Runner의 기본 동작을 유지합니다.

### 기본 템플릿과 슬라이드 제한

```json
{
  "presenter": {
    "default_template": "auto",
    "max_slides": 10
  }
}
```

- `default_template`: 도구 호출에서 템플릿을 생략했을 때 사용할 값
- `max_slides`: Presenter가 만들 수 있는 최대 슬라이드 수, 허용 범위 1~20

유효하지 않은 템플릿이나 범위를 벗어난 `max_slides`는 안전한 기본값 `auto`, `10`으로 정규화됩니다. 실제 덱은 최소 4장이어야 합니다.

## 역할별 모델 배정

일반 서브에이전트는 각각 모델과 thinking을 따로 설정할 수 있습니다.

```json
{
  "subagents": {
    "planner": { "model": "gemma4:e4b", "thinking": false },
    "researcher": { "model": "gemma4:12b", "thinking": false },
    "coder": { "model": "qwen3.8:27b", "thinking": false },
    "tester": { "model": "gemma4:e4b", "thinking": false },
    "reviewer": { "model": "gemma4:12b", "thinking": false },
    "documenter": { "model": "gemma4:12b", "thinking": false },
    "presenter": { "model": "gemma4:12b", "thinking": false }
  }
}
```

기본 배정의 의도는 다음과 같습니다.

- **Coder**: 긴 소스 생성과 수정 능력이 강한 모델
- **Reviewer·Researcher·Documenter**: 맥락 이해와 설명이 안정적인 모델
- **Planner·Tester**: 짧고 구조화된 결과를 빠르게 내는 모델
- **Presenter**: 레이아웃 그룹화와 시각적 구성 감각이 좋은 모델

개발팀 내부 역할은 별도의 `development_team` 설정을 사용합니다. Architect, Cassandra, Detail Planner, Test Coder, 네 전문 Reviewer를 서로 다른 모델로 배정할 수 있습니다.

## 검증과 실패 처리

Presenter는 다음 조건을 만족해야 성공합니다.

1. 모델 응답이 엄격한 `PresentationPlan` JSON이어야 합니다.
2. 템플릿과 레이아웃 조합이 허용 목록에 있어야 합니다.
3. 제목, 본문 항목, 슬라이드 수가 분량 제한을 만족해야 합니다.
4. 파일명이 워크스페이스 안의 단순한 `.html` 이름이어야 합니다.
5. Renderer가 HTML을 생성하고 일반 파일로 저장해야 합니다.
6. 최종 보고서가 실제 HTML artifact 경로를 포함해야 합니다.

구조화 출력이 잘못되면 한 번만 보정을 요청합니다. 보정 후에도 유효하지 않으면 임의로 추측하지 않고 실패합니다.

## 문제 해결

### Presenter가 제한시간을 초과함

- Presenter에 `thinking: false`가 설정됐는지 확인합니다.
- `ollama list`에서 설정한 모델이 설치되어 있는지 확인합니다.
- 슬라이드 수를 줄이고 요청의 근거 자료를 간결하게 만듭니다.
- O.L.L.I.의 Ollama HTTP 제한시간은 긴 Coder 역할과 충돌하지 않도록 40분으로 설정되어 있습니다.

### 요청한 템플릿과 다른 결과가 나옴

명시적 `template` 인자를 사용하면 Presenter가 다른 템플릿을 반환했을 때 검증 단계에서 실패합니다. 자연어 본문에만 템플릿 이름을 쓰는 것보다 도구 인자로 지정하는 것이 확실합니다.

### 결과가 피상적임

구체적인 사실과 발표 목적을 추가합니다. "멋진 발표"보다 다음처럼 요청하는 편이 낫습니다.

```text
Counter 스모크는 전체 제품 경로를 통과했고 Tetris 스모크는 약 69분에 통과했다.
수치가 없는 성능 향상 주장은 제외하고, 이 두 결과를 evidence 슬라이드에 넣어줘.
```

### 기존 HTML을 갱신하고 싶음

같은 파일명을 요청하면 Renderer가 워크스페이스 안의 기존 일반 파일을 교체합니다. 심볼릭 링크나 워크스페이스 밖 경로는 거부됩니다.

## 개발자 검증

일반 회귀 테스트와 정적 검사는 안전 래퍼로 실행합니다.

```bash
scripts/safe-test ./...
scripts/safe-exec __GO__ vet ./...
```

실제 Gemma Presenter 스모크는 로컬 Ollama만 허용한 일회용 샌드박스에서 실행합니다.

```bash
OLLI_SAFE_NETWORK_MODE=ollama \
OLLI_PRESENTER_SMOKE=1 \
OLLI_PRESENTER_MODEL=gemma4:12b \
scripts/safe-test ./subagent \
  -run TestPresenterTemplateSmoke \
  -count=1 \
  -timeout=30m \
  -v
```

스모크 작업 공간과 생성 HTML은 테스트 종료 시 삭제됩니다. 실제 사용에서는 현재 O.L.L.I. 워크스페이스에 결과가 남습니다.

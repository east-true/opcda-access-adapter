# ADR-0020: 명시적으로 선택하는 DA-native MCP stdio 프런트엔드

- 상태: 승인
- 날짜: 2026-10-27
- 이슈: [#226](https://github.com/east-true/opcda-access-adapter/issues/226)

## 배경

현재 기능을 AI 클라이언트에 제공하는 MCP를 같은 레포의 추가 프런트엔드로 구현한다.
설계 §22를 적용하며 원본은 하나의 로컬 OPC DA 서버다. 별도 브리지·다중 소스·집계·
자산 모델·새 소스·영속 값 저장·자동 제어는 추가하지 않는다.

## 결정

1. `mcp`는 HTTP·gRPC·OPC UA와 배타적인 선택이다. 기본값은 `http`이며 명시적
   `mcp` 선택 전에는 MCP 서버·세션·전송을 생성하지 않는다. 선택하면 같은 프로세스의
   DA Runtime을 직접 호출하고 네트워크 리스너는 만들지 않는다.
2. 전송은 클라이언트가 실행 파일을 시작하는 stdio다. stdout은 한 줄에 하나인 JSON-RPC
   메시지 전용이며 진단은 stderr로만 보낸다. SDK의 기본 폐기 로거를 유지하고 wire logging을
   사용하지 않는다. Windows Service 설치를 거부하며 setup에서는 저장만 제공한다.
3. 설정 버전 4의 `frontend`는 `{"type":"mcp"}`다. 버전 1~3도 계속 읽지만 MCP 선택은
   버전 4부터 허용한다. 다른 프런트엔드 주소나 UA 설정을 섞으면 거부한다.
4. 도구는 Status·Browse·Device Read·엄격한 value-only Write·Available Item Properties·
   Item Properties·구독 생성/조회/해제다. 고정 입력 스키마를 검증하며 정확한 ItemID·순서·
   부분 오류·VARTYPE·raw Quality·시각 존재 여부·HRESULT·접근 권한을 유지한다.
5. HTTP와 DA 전용 JSON 값 코덱을 공유한다. 64비트 정수는 십진 문자열, 비유한 실수는
   명시적 이름, SAFEARRAY는 원소 타입·차원별 하한/길이·원소 순서를 보존한다.
   요청을 HTTP나 gRPC 프런트엔드로 우회시키지 않는다.
6. MCP 도구는 DA 이벤트 스트림이 아니다. 구독 생성은 세션 소유 DA 그룹 하나를 만들고,
   next는 코어의 기존 항목별 병합 집합을 직접 Drain한다. 추가 큐·재샘플링·drop counter·
   자동 재구독·재생을 넣지 않는다. 느린 소비자 때문에 그룹을 종료하지 않는다.
   빈 대기 만료는 빈 결과이고, 무효화는 `SUBSCRIPTION_INVALIDATED`다.
   제거됐거나 다른 세션의 핸들은 `SUBSCRIPTION_NOT_FOUND`다. 동일 핸들의 병렬 next는 거부한다.
7. 명시적 해제와 EOF·취소·전송 실패는 소유 그룹을 정리하며 이후 Runtime도 종료한다.
   연결 세대가 바뀌어도 구독을 복원하지 않는다. 취소된 Write가 실행되지 않았다고 추론하지 않는다.
8. Write 기본 비활성화·value-only·엄격한 타입·자동 재시도 금지를 유지한다.
   도구 annotation은 설명용 힌트이며 권한 통제가 아니다. 별도 승인 UI나 자동 판단 정책은 넣지 않는다.
9. 입력 한 줄 1 MiB, 출력 한 줄 4 MiB, 미완료 요청 16개, JSON 깊이 64를 제한한다.
   batch·중복 키·잘못된 UTF-8/surrogate·과대 프레임·중복 미완료 ID는 세션 오류다.
   도구 실패는 `isError` 및 계층·코드·원본 HRESULT로 반환하고 입력·값을 오류에 포함하지 않는다.
   작업은 기존 요청 제한 시간(기본 10초)을 따른다. next의 `waitMs`는 최대 30초지만
   요청 제한 시간이 먼저 끝나면 취소된다.
10. 공식 `github.com/modelcontextprotocol/go-sdk v1.8.0`과
    `github.com/google/jsonschema-go v0.4.3`을 고정한다. SDK가 협상·취소·도구 프로토콜을,
    어댑터가 SDK 비동기 큐 앞에서 요청 수와 프레임 크기를 제한한다.
    SDK의 기본 폐기 로거를 확인했으며 HTTP·OAuth·리소스·프롬프트·샘플링 기능은 공개하지 않는다.

## 의존성과 배포 영향

추가 모듈은 위 두 모듈과 SDK의 JSON 구현인 `github.com/segmentio/encoding v0.5.4`,
`github.com/segmentio/asm v1.1.3`, URI template인 `github.com/yosida95/uritemplate/v3 v3.0.2`,
`golang.org/x/oauth2 v0.36.0`, `golang.org/x/sync v0.22.0`, `golang.org/x/time v0.15.0`이다.
OPC SDK나 새 산업 소스 의존성은 없다. 라이선스 원문과 버전은 `THIRD_PARTY_NOTICES.md`에,
허용 이유는 의존성 허용 목록에 기록한다. SDK LICENSE의 Apache-2.0 이행 및 기존 MIT
기여 조건을 단일 라이선스로 축약하지 않고 원문 전체를 포함한다.

모든 프런트엔드를 하나의 실행 파일로 배포하므로 MCP를 선택하지 않아도 바이너리와 의존성은
증가한다. 명시적 선택 전에는 실행 중인 MCP 세션이나 MCP 네트워크 리스너가 생기지 않는다.

같은 로컬 Go 1.26.0에서 기본 `go build`로 비교한 실행 파일은 386에서
19,270,144 → 22,624,768 bytes, amd64에서 20,131,328 → 23,719,936 bytes였다.
이 수치는 strip하지 않은 로컬 빌드의 관측이며 릴리스 ZIP 크기나 메모리 사용량이 아니다.

## 검증과 대안

Go SDK 클라이언트 통합 테스트로 도구·값 표현·부분 실패·Write 정책·구독 소유권·병합·무효화·
EOF 정리를 검증한다. 독립 Python 클라이언트로 실제 실행 파일의 stdio를 검사하고 이를 Linux
CI와 기존 Windows 386·amd64 실제 DA fixture 절차에 연결한다. 워크플로 구성 자체를 실제
DA 검증 결과로 주장하지 않으며 실행 결과는 호환성 문서에 별도로 기록한다.

별도 gRPC 브리지는 초기 목적에 불필요한 배포·API 버전 계약을 추가하므로 선택하지 않는다.
Streamable HTTP는 현재 요구에 없는 네트워크 경계를 추가하므로 제외한다.
직접 JSON-RPC 구현 대신 공식 SDK를 쓰되 전송 자원 한계는 어댑터가 책임진다.
MCP 리소스 변경 알림을 DA callback으로 가장하지 않고 명시적 그룹 도구를 제공한다.

## 참고

- [MCP 도구](https://modelcontextprotocol.io/specification/2025-11-25/server/tools)
- [MCP 전송](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports)
- [공식 SDK 고정 태그](https://github.com/modelcontextprotocol/go-sdk/tree/v1.8.0)

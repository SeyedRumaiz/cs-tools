# AI assistant providers

The chat panel (`<wso2-live-chat>`) answers a customer with an AI assistant first and hands over to a support engineer in the same panel when they ask. The bridge decides which assistant answers, per product (tenant):

| Provider | What answers | Set with |
|---|---|---|
| `novera` | Novera, the WSO2 support assistant: Claude with WSO2 knowledge-base and web search. The default when `NOVERA_WS_BASE_URL` is set. | `NOVERA_WS_BASE_URL`, `NOVERA_TOKEN_URL`, `NOVERA_CLIENT_ID`, `NOVERA_CLIENT_SECRET`, `NOVERA_SCOPES` |
| `http` | The product's own AI service, with its own model, knowledge and tools. | `<prefix>HTTP_URL` and auth, below |
| `mock` | A fixed placeholder answer, for local development. | |
| `off` | No assistant: the panel is the engineer chat only. | |

`ASSISTANT_PROVIDER` is the default for every tenant. A tenant overrides it with `TENANT_<SLUG>_ASSISTANT_PROVIDER`, where `<SLUG>` is the slug in upper snake case (`identity-console` becomes `IDENTITY_CONSOLE`). `ASSISTANT_DISABLED_TENANTS` lists tenants with none. The bridge checks these settings at startup and logs each tenant's provider.

```env
# Novera for everyone, except Devant, which uses its own service.
NOVERA_WS_BASE_URL=wss://...
NOVERA_TOKEN_URL=https://.../oauth2/token
NOVERA_CLIENT_ID=...
NOVERA_CLIENT_SECRET=...
TENANT_DEVANT_ASSISTANT_PROVIDER=http
TENANT_DEVANT_ASSISTANT_HTTP_URL=https://devant-ai.example.com/support/ask
TENANT_DEVANT_ASSISTANT_HTTP_TOKEN=...
```

## Plugging in your own AI service (`http`)

Your service receives one request per customer question and answers it. It keeps each conversation's history itself, keyed by `conversationId`, so follow-up questions make sense.

### Request

```http
POST <your URL>
Content-Type: application/json
Accept: text/event-stream, application/json
Authorization: Bearer <token>

{
  "conversationId": "devant-support-ai-1791454347308",
  "accountId": "devant.3f9a1c0e5b7d2a46",
  "tenant": "devant",
  "message": "How do I deploy an integration?"
}
```

- `conversationId` identifies the conversation; the same customer keeps it across questions and page reloads.
- `accountId` identifies the customer without revealing who they are: the tenant plus a hash of their user ID. Use it for per-user limits.
- The message is at most 4,000 bytes. The bridge sends one question per conversation at a time.

Auth, from the `<prefix>` settings (`ASSISTANT_` or `TENANT_<SLUG>_ASSISTANT_`):

| Settings | Authorization header |
|---|---|
| `HTTP_TOKEN_URL`, `HTTP_CLIENT_ID`, `HTTP_CLIENT_SECRET`, `HTTP_SCOPES` | An OAuth2 client credentials token |
| `HTTP_TOKEN` | That fixed token |
| neither | None |

### Response

Either stream the answer as Server-Sent Events (`Content-Type: text/event-stream`), each `data:` line one event:

```text
data: {"type":"status","text":"Checking your project"}

data: {"type":"token","text":"Open "}

data: {"type":"token","text":"**Deploy** in the left menu."}

data: {"type":"done","text":"Open **Deploy** in the left menu."}
```

| `type` | Meaning |
|---|---|
| `status` | Progress shown while you work, such as "Searching the docs". Optional. |
| `token` | The next piece of the answer. |
| `done` | The answer is complete; `text` is the whole answer (or empty to use the streamed pieces). |
| `error` | You could not answer; `text` is shown to the customer, so keep it friendly. |

Or, if you do not stream, answer with one JSON object (`Content-Type: application/json`):

```json
{ "answer": "Open **Deploy** in the left menu." }
```

Answers are Markdown: paragraphs, lists, **bold**, `code`, fenced code blocks and http(s) links are shown formatted.

Any non-2xx status, or a reply that is neither of the above, shows the customer "The assistant is not available right now." A stream that ends without `done` still counts as an answer if it sent some text.

### Example service

`cmd/example-assistant` is a runnable example that streams a reply and remembers each conversation. To try the `http` provider locally:

```bash
go run ./cmd/example-assistant   # listens on :8095
```

```env
TENANT_DEVANT_ASSISTANT_PROVIDER=http
TENANT_DEVANT_ASSISTANT_HTTP_URL=http://localhost:8095/ask
```

### Minimal example (Python, FastAPI)

```python
from fastapi import FastAPI
from pydantic import BaseModel

app = FastAPI()
history: dict[str, list[dict]] = {}

class Question(BaseModel):
    conversationId: str
    accountId: str
    tenant: str
    message: str

@app.post("/support/ask")
def ask(q: Question):
    turns = history.setdefault(q.conversationId, [])
    turns.append({"role": "user", "content": q.message})
    answer = my_ai(turns)  # your model, knowledge and tools
    turns.append({"role": "assistant", "content": answer})
    return {"answer": answer}
```

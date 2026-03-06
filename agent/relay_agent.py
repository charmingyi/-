from urllib.parse import unquote, urlparse

import httpx
from fastapi import FastAPI, Header, HTTPException, Request, Response

app = FastAPI(title="Emby Relay Agent")

# 实际部署时建议用环境变量或配置文件
AGENT_TOKEN = "change-me"


@app.api_route("/relay", methods=["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"])
async def relay(target: str, request: Request, x_relay_token: str = Header(default="")):
    if x_relay_token != AGENT_TOKEN:
        raise HTTPException(status_code=403, detail="invalid relay token")

    decoded = unquote(target)
    parsed = urlparse(decoded)
    if parsed.scheme not in {"http", "https"}:
        raise HTTPException(status_code=400, detail="invalid target url")

    body = await request.body()
    passthrough_headers = {
        "user-agent": request.headers.get("user-agent", "relay-agent"),
        "content-type": request.headers.get("content-type", ""),
    }

    async with httpx.AsyncClient(timeout=120) as client:
        upstream = await client.request(request.method, decoded, headers=passthrough_headers, content=body)

    excluded = {"content-encoding", "transfer-encoding", "connection", "keep-alive"}
    response_headers = {k: v for k, v in upstream.headers.items() if k.lower() not in excluded}
    return Response(content=upstream.content, status_code=upstream.status_code, headers=response_headers)

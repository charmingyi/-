from __future__ import annotations

from typing import Optional
from urllib.parse import quote

import httpx
from fastapi import Depends, FastAPI, Form, HTTPException, Request
from fastapi.responses import HTMLResponse, RedirectResponse, Response
from fastapi.templating import Jinja2Templates
from sqlmodel import Field, Session, SQLModel, create_engine, select

app = FastAPI(title="Emby Relay Hub")
templates = Jinja2Templates(directory="templates")
engine = create_engine("sqlite:///relayhub.db", connect_args={"check_same_thread": False})


class Agent(SQLModel, table=True):
    id: Optional[int] = Field(default=None, primary_key=True)
    name: str
    relay_url: str
    token: str
    notes: str = ""


class Upstream(SQLModel, table=True):
    id: Optional[int] = Field(default=None, primary_key=True)
    name: str
    base_url: str
    notes: str = ""


class Route(SQLModel, table=True):
    id: Optional[int] = Field(default=None, primary_key=True)
    name: str
    slug: str = Field(index=True, unique=True)
    agent_id: int = Field(foreign_key="agent.id")
    upstream_id: int = Field(foreign_key="upstream.id")


def get_session():
    with Session(engine) as session:
        yield session


@app.on_event("startup")
def on_startup() -> None:
    SQLModel.metadata.create_all(engine)


@app.get("/", response_class=HTMLResponse)
def index(request: Request, session: Session = Depends(get_session)):
    agents = session.exec(select(Agent).order_by(Agent.id.desc())).all()
    upstreams = session.exec(select(Upstream).order_by(Upstream.id.desc())).all()
    routes = session.exec(select(Route).order_by(Route.id.desc())).all()

    agent_map = {a.id: a for a in agents}
    upstream_map = {u.id: u for u in upstreams}

    return templates.TemplateResponse(
        request,
        "index.html",
        {
            "agents": agents,
            "upstreams": upstreams,
            "routes": routes,
            "agent_map": agent_map,
            "upstream_map": upstream_map,
        },
    )


@app.post("/agents")
def create_agent(
    name: str = Form(...),
    relay_url: str = Form(...),
    token: str = Form(...),
    notes: str = Form(""),
    session: Session = Depends(get_session),
):
    session.add(Agent(name=name, relay_url=relay_url.rstrip("/"), token=token, notes=notes))
    session.commit()
    return RedirectResponse("/", status_code=303)


@app.post("/upstreams")
def create_upstream(
    name: str = Form(...),
    base_url: str = Form(...),
    notes: str = Form(""),
    session: Session = Depends(get_session),
):
    session.add(Upstream(name=name, base_url=base_url.rstrip("/"), notes=notes))
    session.commit()
    return RedirectResponse("/", status_code=303)


@app.post("/routes")
def create_route(
    name: str = Form(...),
    slug: str = Form(...),
    agent_id: int = Form(...),
    upstream_id: int = Form(...),
    session: Session = Depends(get_session),
):
    slug = slug.strip().lower()
    existing = session.exec(select(Route).where(Route.slug == slug)).first()
    if existing:
        raise HTTPException(status_code=400, detail="slug already exists")
    session.add(Route(name=name, slug=slug, agent_id=agent_id, upstream_id=upstream_id))
    session.commit()
    return RedirectResponse("/", status_code=303)


@app.api_route("/proxy/{slug}/{path:path}", methods=["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"])
async def proxy_request(slug: str, path: str, request: Request, session: Session = Depends(get_session)):
    route = session.exec(select(Route).where(Route.slug == slug)).first()
    if not route:
        raise HTTPException(status_code=404, detail="route not found")

    agent = session.get(Agent, route.agent_id)
    upstream = session.get(Upstream, route.upstream_id)
    if not agent or not upstream:
        raise HTTPException(status_code=500, detail="route data broken")

    target_url = f"{upstream.base_url}/{path}"
    if request.url.query:
        target_url += f"?{request.url.query}"

    relay_url = f"{agent.relay_url}/relay?target={quote(target_url, safe=':/?&=%')}"
    headers = {
        "x-relay-token": agent.token,
        "user-agent": request.headers.get("user-agent", "relay-hub"),
    }
    body = await request.body()

    async with httpx.AsyncClient(timeout=120) as client:
        relay_resp = await client.request(
            request.method,
            relay_url,
            headers=headers,
            content=body,
        )

    excluded = {"content-encoding", "transfer-encoding", "connection", "keep-alive"}
    response_headers = {
        key: value
        for key, value in relay_resp.headers.items()
        if key.lower() not in excluded
    }
    return Response(content=relay_resp.content, status_code=relay_resp.status_code, headers=response_headers)

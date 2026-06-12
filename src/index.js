// benchy.run worker: serves the static site plus the global leaderboard API.
//
// POST /api/results     — opt-in submissions from `bench run` (report_results)
// GET  /api/leaderboard — models ranked by run-weighted mean composite
// GET  /dl/<binary>     — CLI release binaries, served from the benchy-dl R2
//                         bucket (too large / too churny for git-backed assets)

const MAX_ROWS_PER_SUBMISSION = 50;
const DL_NAME = /^benchy?-(darwin|linux)-(amd64|arm64)$/;

export default {
  async fetch(request, env) {
    const url = new URL(request.url);

    if (url.pathname === "/api/results" && request.method === "POST") {
      return postResults(request, env);
    }
    if (url.pathname === "/api/leaderboard" && request.method === "GET") {
      return getLeaderboard(env);
    }
    if (url.pathname === "/api/version" && request.method === "GET") {
      return getVersion(env);
    }
    if (url.pathname.startsWith("/api/")) {
      return json({ error: "not found" }, 404);
    }
    if (url.pathname.startsWith("/dl/") && request.method === "GET") {
      return getBinary(url.pathname.slice(4), env);
    }
    return env.ASSETS.fetch(request);
  },
};

// getVersion serves the latest CLI release tag for `benchy update` / the
// daily update hint. Source of truth is latest.json in the benchy-dl bucket,
// uploaded alongside each release's binaries.
async function getVersion(env) {
  const obj = await env.DL.get("latest.json");
  if (!obj) {
    return json({ error: "no version published" }, 404);
  }
  return new Response(obj.body, {
    headers: { "Content-Type": "application/json", "Cache-Control": "public, max-age=300" },
  });
}

async function getBinary(name, env) {
  if (!DL_NAME.test(name)) {
    return new Response("not found", { status: 404 });
  }
  const obj = await env.DL.get(name);
  if (!obj) {
    return new Response("not found", { status: 404 });
  }
  return new Response(obj.body, {
    headers: {
      "Content-Type": "application/octet-stream",
      "Content-Length": String(obj.size),
      "Content-Disposition": `attachment; filename="${name}"`,
      "Cache-Control": "public, max-age=300",
      ETag: obj.httpEtag,
    },
  });
}

async function postResults(request, env) {
  let body;
  try {
    body = await request.json();
  } catch {
    return json({ error: "invalid JSON" }, 400);
  }
  const judge = typeof body.judge === "string" ? body.judge.slice(0, 200) : "";
  const rows = Array.isArray(body.rows) ? body.rows.slice(0, MAX_ROWS_PER_SUBMISSION) : [];
  const valid = rows.filter(
    (r) =>
      r &&
      typeof r.model === "string" &&
      r.model.length > 0 &&
      r.model.length <= 200 &&
      Number.isFinite(r.score) &&
      r.score >= 0 &&
      r.score <= 10 &&
      Number.isInteger(r.runs) &&
      r.runs > 0 &&
      r.runs <= 10000
  );
  if (valid.length === 0) {
    return json({ error: "no valid rows" }, 400);
  }
  const stmt = env.DB.prepare(
    "INSERT INTO submissions (model, score, runs, judge) VALUES (?1, ?2, ?3, ?4)"
  );
  await env.DB.batch(valid.map((r) => stmt.bind(r.model, r.score, r.runs, judge)));
  return json({ ok: true, accepted: valid.length });
}

async function getLeaderboard(env) {
  const { results } = await env.DB.prepare(
    `SELECT model,
            ROUND(SUM(score * runs) / SUM(runs), 2) AS score,
            SUM(runs) AS runs,
            COUNT(*) AS submissions
       FROM submissions
      GROUP BY model
      ORDER BY score DESC, model ASC
      LIMIT 100`
  ).all();
  return json({ leaderboard: results }, 200, {
    "Cache-Control": "public, max-age=60",
    "Access-Control-Allow-Origin": "*",
  });
}

function json(obj, status = 200, headers = {}) {
  return new Response(JSON.stringify(obj), {
    status,
    headers: { "Content-Type": "application/json", ...headers },
  });
}

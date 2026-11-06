// ClipSetup web: the Apple App Site Association file the App Clip needs, a small landing page and a privacy
// policy. No dependencies, no storage, no logging of query strings.

const REPO = "https://github.com/narduk-enterprises/clipsetup";

// Served byte for byte; Apple fetches this to bind https://setup.nard.uk/d* to the App Clip and the app.
const AASA =
  '{"appclips":{"apps":["FVSY7CFC3S.com.narduk.devices.Clip"]},"applinks":{"details":[{"appIDs":["FVSY7CFC3S.com.narduk.devices"],"components":[{"/":"/d*"}]}]}}';

function page(title, body) {
  return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>${title}</title>
<style>
body{font:17px/1.5 -apple-system,system-ui,sans-serif;max-width:34em;margin:3em auto;padding:0 1.2em;color:#1d1d1f}
a{color:#0066cc}
@media (prefers-color-scheme:dark){body{background:#000;color:#f5f5f7}a{color:#4da3ff}}
</style>
</head>
<body>
<h1>${title}</h1>
${body}
</body>
</html>
`;
}

const HOME = page(
  "ClipSetup",
  `<p>ClipSetup: put an ESP32 on Wi-Fi from an iPhone. Scan the device's QR with the iPhone Camera.</p>
<p><a href="${REPO}">${REPO}</a></p>
<p><a href="/privacy">Privacy policy</a></p>`,
);

const DEVICE = page(
  "ClipSetup",
  `<p>Open this link on an iPhone to set up the device.</p>
<p>ClipSetup: put an ESP32 on Wi-Fi from an iPhone. Scan the device's QR with the iPhone Camera.</p>
<p><a href="${REPO}">${REPO}</a></p>`,
);

const PRIVACY = page(
  "ClipSetup privacy policy",
  `<p>The ClipSetup app and App Clip collect no data. They have no accounts, no analytics and no servers.</p>
<p>The Wi-Fi password goes only from your phone to the device, over an encrypted Bluetooth session. If you
choose to, it is remembered in your phone's Keychain.</p>
<p>Questions: open an issue at <a href="${REPO}/issues">${REPO}/issues</a>.</p>`,
);

const HTML = { "content-type": "text/html; charset=utf-8" };
const COMMON = { "cache-control": "public, max-age=300", "x-content-type-options": "nosniff" };

function respond(request, body, headers, status = 200) {
  return new Response(request.method === "HEAD" ? null : body, {
    status,
    headers: { ...COMMON, ...headers },
  });
}

export default {
  fetch(request) {
    if (request.method !== "GET" && request.method !== "HEAD") {
      return new Response("Method not allowed", { status: 405, headers: { allow: "GET, HEAD" } });
    }
    const { pathname } = new URL(request.url);
    switch (pathname) {
      case "/.well-known/apple-app-site-association":
        return respond(request, AASA, { "content-type": "application/json" });
      case "/":
        return respond(request, HOME, HTML);
      case "/d":
        return respond(request, DEVICE, HTML);
      case "/privacy":
        return respond(request, PRIVACY, HTML);
      default:
        return respond(request, "Not found\n", { "content-type": "text/plain; charset=utf-8" }, 404);
    }
  },
};

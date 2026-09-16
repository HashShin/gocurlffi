# Google's JavaScript gate and BotGuard: what was measured

This is the record of an investigation, kept because the measurements are worth
more than the conclusion. The short version: **`gocurlffi` cannot search Google,
and the reason is not the fingerprint, the JavaScript engine, or the token
handling.** A real Chromium from the same host is refused the same way.

## The gate

`https://www.google.com/search?q=...` returns a "Turn on JavaScript to keep
searching" page (about 92 KB) to every client that does not execute scripts.
Plain `curl` receives the same page, so this is not a client bug and no
impersonation target changes it. The `gocurlffi` CLI detects the interstitial
and prints a `warning:` rather than letting it be mistaken for content.

Server-rendered alternatives that do return linkable HTML:

- `https://www.bing.com/search?q=...`
- `https://search.brave.com/search?q=...`
- `https://lite.duckduckgo.com/lite/?q=...`

## How far the browser gets

The interstitial carries Google's BotGuard program - pure JavaScript under
`window.knitsail`, with no WebAssembly. The pure-Go browser runs it to
completion:

- the challenge callback fires, rather than the `sg_b_e` error beacon;
- no console error is raised;
- `SG_SS` is minted, a 0.8-1.2 KB token;
- the page then hands off with `location.replace(...)`, which the browser
  follows. That handoff carries the token in the `SG_SS` cookie, not the query
  string (`S()` strips `sg_ss` and adds only `sei`), and the cookie jar hands it
  back byte for byte.

## What Google answers with

`429` and a CAPTCHA reading "our systems have detected unusual traffic from your
computer network ... the block will expire shortly after those requests stop".

That wording points at the network, and it is misleading. Four controls, each
varying one thing:

1. **Two `/search` requests in one session with JavaScript disabled**, so no
   token is ever minted, both return the ordinary interstitial - repeatedly,
   either side of the token path being blocked. The IP is not rate-limited.
2. **The challenge run with JavaScript on, but `SG_SS` cleared from the jar**
   before the follow-up request - same session, same cookie set, same burst of
   subresources and beacons - returns the ordinary interstitial. The burst is not
   the trigger either.
3. **Forcing the other handoff branch.** Google's code is
   `ss_cgi || document.cookie.indexOf("SG_SS=") < 0 ? T(a) : U(S())`, where `T`
   puts the token in the query and `U` leaves it in the cookie. Sending
   `sg_ss=<token>&sei=<id>` in the URL instead is answered the same way.
4. **A bogus `SG_SS` cookie, and a bogus `sei` parameter**, are both ignored -
   each still returns the ordinary interstitial.

Control 4 is the informative one. The CAPTCHA is not a response to "a token was
presented", because a token the server cannot parse is dropped. The token this
browser mints is parsed and then escalated, which means it is structurally sound
and it is the **environment verdict it carries** that fails.

## Ruled out

The request is well formed, the VM reports success, and the cookie round-trips
byte for byte. All of the following make no difference:

- query flags `gbv=1` and `udm=14`;
- every impersonation target;
- a warmed cookie jar;
- corrected binary/base64 handling, text-encoding and cookie layers;
- **a real Chromium.** Chrome 153 from this host gets the CAPTCHA too, which is
  what moves the blocker off the browser and onto the network's reputation.

## Why there is no fix here

What Google's server disagrees with is the environment that verdict describes,
inside an encrypted blob of roughly 800 bytes for which this repository has no
reference. Forging that token is not something this project will do: it is
deliberately circumventing an anti-automation control, and it would need a
reverse-engineered specification that would rot immediately.

If the goal is HTML search results, use one of the alternatives above. If the
goal is to understand the challenge, the measurements in this file are the
useful part.

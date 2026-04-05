# SEO & Discoverability Checklist

## 1. GitHub Repository Topics

Go to: https://github.com/AlchemyLink/Raven-subscribe → Settings → scroll to "Topics"

Add these topics (copy-paste each):
```
xray xray-core vless vmess trojan shadowsocks hysteria2 subscription-server sing-box reality xhttp v2ray vpn proxy golang self-hosted
```

---

## 2. GitHub Repository Description

Go to: https://github.com/AlchemyLink/Raven-subscribe → Settings (gear icon next to "About")

Set description to:
```
Self-hosted subscription server for Xray-core and sing-box. Auto-discovers users, serves personal VPN config URLs for V2RayNG, NekoBox, Hiddify, Hysteria2.
```

Set website to your server URL or leave empty.

---

## 3. awesome-selfhosted

awesome-selfhosted is the most important list for self-hosted projects.
Repo: https://github.com/awesome-selfhosted/awesome-selfhosted

The entry to add (goes under **Proxy** section, alphabetically near "R"):

```markdown
- [Raven Subscribe](https://github.com/AlchemyLink/Raven-subscribe) - Self-hosted subscription server for Xray-core and sing-box. Auto-discovers VLESS/VMess/Trojan/Shadowsocks/Hysteria2 users from server configs and serves personal subscription URLs for V2RayNG, NekoBox, Hiddify and other clients. `MIT` `Go/Docker`
```

PR checklist (from their CONTRIBUTING.md):
- [ ] Fork awesome-selfhosted/awesome-selfhosted
- [ ] Add the line above in `README.md` under **Proxy** section, in alphabetical order
- [ ] The project must have a working demo or clear screenshots (add one to your README if missing)
- [ ] License must be in the repo (MIT — already present)
- [ ] Must have a public source code link (already present)

---

## 4. awesome-v2ray / awesome-xray lists

Add to: https://github.com/v2fly/awesome-v2ray

Entry:
```markdown
- [Raven Subscribe](https://github.com/AlchemyLink/Raven-subscribe) - Self-hosted subscription server. Auto-discovers users from Xray server configs, serves Xray JSON / sing-box JSON / share links.
```

---

## 5. Done automatically (this PR)

- [x] README.md first paragraph rewritten with SEO keywords
- [x] README.ru.md first paragraph rewritten with SEO keywords
- [x] Both mention: self-hosted, Xray-core, sing-box, VLESS, VMess, Trojan, Shadowsocks, Hysteria2, XHTTP, REALITY, V2RayNG, NekoBox, Hiddify

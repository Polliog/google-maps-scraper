# Proxy Sponsors

Google Maps scraping can trigger rate limits or blocking, especially with larger jobs or higher concurrency. Proxies can help, but they are not a guarantee. Proxy quality, geography, concurrency, query volume, and Google behavior all affect reliability.

This page lists current proxy sponsors and supporters of this project. Using these links helps fund maintenance.

## Configure Proxies

Use `-proxies` with a comma-separated list:

```bash
./google-maps-scraper \
  -input queries.txt \
  -results results.csv \
  -proxies "socks5://user:pass@host:port,http://host2:port2" \
  -depth 1
```

Supported protocols: `socks5`, `socks5h`, `http`, `https`.

### Default proxies via environment variable

If the `-proxies` flag is not set, the scraper falls back to the `PROXIES`
environment variable (same comma-separated format). This is handy for setting a
default proxy once — e.g. in Docker — without repeating the flag on every run.

Precedence: `-proxies` flag > `PROXIES` env var > no proxy.

```bash
export PROXIES="http://user:pass@host:port"
./google-maps-scraper -input queries.txt -results results.csv -depth 1
```

## Docker Example

```bash
mkdir -p gmaps-output

docker run \
  -v gmaps-playwright-cache:/opt \
  -v "$PWD/example-queries.txt:/queries.txt:ro" \
  -v "$PWD/gmaps-output:/out" \
  gosom/google-maps-scraper \
  -input /queries.txt \
  -results /out/results.csv \
  -depth 1 \
  -proxies "http://user:pass@host:port,socks5://host:port" \
  -exit-on-inactivity 3m
```

Or set a default proxy for the container with `-e PROXIES=...` instead of the flag:

```bash
docker run -d \
  -v "$PWD/gmapsdata:/gmapsdata" \
  -p 8999:8080 \
  -e PROXIES="http://user:pass@host:port" \
  gosom/google-maps-scraper \
  -data-folder /gmapsdata
```

## Current Proxy Sponsors

| Provider | Notes | Link |
|---|---|---|
| NetNut | Premium residential proxy network and web data collection infrastructure | [Visit NetNut](https://netnut.io/?ref=y2fmmzz) / [Learn more](../netnut.md) |
| RapidProxy | Residential proxy provider supporting this project | [Visit RapidProxy](https://www.rapidproxy.io/?ref=gosom) |
| Webshare | Proxy provider with HTTP and SOCKS5 support | [Visit Webshare](https://www.webshare.io/?referral_code=0q3l81eet8mp) |
| BirdProxies | Residential and ISP proxy provider supporting this project | [Visit BirdProxies](https://birdproxies.com/?utm_source=github&utm_medium=sponsorship&utm_campaign=gosom-google-maps-scraper) / [Discord](https://discord.com/invite/birdproxies) |
| Decodo | Proxy provider supporting this project | [Visit Decodo](https://visit.decodo.com/APVbbx) |
| Evomi | Proxy provider supporting this project | [Visit Evomi](https://evomi.com?utm_source=github&utm_medium=banner&utm_campaign=gosom-maps) |

## Practical Notes

- Test with a small input file before running a large job.
- Increase concurrency gradually.
- If results become less reliable, reduce `-c` before assuming the proxy provider is the only issue.
- Keep proxy URLs private. Do not commit credentials.

# Cloudflare token validation

The official [cloudflared create implementation](https://github.com/cloudflare/cloudflared/blob/master/cmd/cloudflared/tunnel/subcommand_context.go)
accepts base64-decoded tunnel secrets of **at least 32 bytes**. The official
[Create Tunnel API](https://developers.cloudflare.com/api/resources/zero_trust/subresources/tunnels/subresources/cloudflared/methods/create/)
also specifies this minimum and does not document a maximum. These sources were
read on 2026-10-04; they do not establish that only 32/64-byte values are legal.

The test NAS acceptance evidence `real-cf-token-validation-shape.json`
reported a 64-byte secret from the default `cf tunnels create` token. The old
exact-32 validator rejected that token before any connector started.

`cloudflare.validateToken` (token mode) accepts the official minimum, with a
4096-byte outer token limit, base64/JSON format, account length and tunnel UUID
checks; the public hostname is checked separately. Larger legal upstream tokens exceeding this local
resource bound remain unsupported. This is a local envelope limit, not an
invented Cloudflare maximum. Validation does not contact Cloudflare or prove a
token is authentic, unrevoked or able to connect.

Invalid tokens produce `invalid_cloudflare_token` through daemon/client/CGI and
a localized UI message containing that code; no submitted token enters error
messages. The UI clears the input on submission. Synthetic tests exercise
31/32/33/64/128/2048-byte secrets, oversized envelopes, private persistence and
restore. The real default token and public connection require a separately authorized
test NAS acceptance; these tests create no Cloudflare resources.

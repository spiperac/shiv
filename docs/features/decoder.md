# Decoder

The Decoder is a quick utility for encoding, decoding, and hashing text without leaving the application. Paste input in the top pane, select an operation, and the result appears in the bottom pane.

## Operations

**URL Encode / Decode** — percent-encodes or decodes query string values using standard URL encoding.

**URL Path Encode / Decode** — same as URL encoding but using path escaping rules, which treats `/` as a safe character.

**Base64 Encode / Decode** — standard Base64. The decoder accepts both standard and URL-safe variants and strips whitespace before decoding.

**Base64 URL-safe Encode / Decode** — encodes using the URL-safe alphabet (`-` and `_` instead of `+` and `/`).

**Hex Encode / Decode** — encodes bytes to hex string and back. Spaces in the input are ignored when decoding.

**HTML Entity Encode / Decode** — escapes `<`, `>`, `&`, and quotes to HTML entities and unescapes them back.

**JSON Pretty / Minify** — pretty-prints JSON with two-space indentation, or minifies it back to a single line.

**JWT Decode (no verify)** — splits a JWT and pretty-prints the header and payload sections. The signature is shown as-is. No signature verification is performed.

**MD5 Hash** — outputs the MD5 hex digest of the input. One-way only.

**SHA1 Hash** — outputs the SHA1 hex digest of the input. One-way only.

**SHA256 Hash** — outputs the SHA256 hex digest of the input. One-way only.

## Workflow

Select the operation from the dropdown. Click the forward button (labelled Encode, Decode, Pretty, or Hash depending on the operation) to transform the input and write the result to the output pane.

For operations that have a reverse (everything except the hash functions and JWT), a second button on the left runs the reverse direction.

**Swap** exchanges the contents of the input and output panes, which is useful for chaining operations.

**Copy Output** copies the output pane to the clipboard.

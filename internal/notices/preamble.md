# Third-party notices

`garmin-mcp` is licensed under the MIT License. See [`LICENSE`](LICENSE)
for the project's own terms. This file records the third-party material
that the project either copied from or links into a released binary, and
reproduces every applicable licence in full. There is no separate
`NOTICE` file: this is the single notices document, and it ships with the
release archives and inside the container image under
`/licenses/garmin-mcp/`.

Licence texts for Go modules are copied from the module cache
(`go env GOMODCACHE`) at the exact versions listed, so each text is the
one that belongs to the pinned version. Upstream compatibility-reference
licences are copied from the pinned commits named below.

Nothing here is a summary. Where a text is reproduced it is byte-for-byte,
with one exception that carries no terms: a run of blank lines at the very
end of a file is collapsed to a single newline so the closing code fence
sits on its own line.

## Upstream compatibility references

### `Taxuspt/garmin_mcp`

- Repository: <https://github.com/Taxuspt/garmin_mcp>
- Pinned commit: `3610be6feed93088d85b0f35aba9d7d07c2505a7`
- SPDX identifier: `MIT`

**Why attribution is owed.** `compat/tools.json` carries the tool
descriptions of the pinned upstream surface, and all 138 of them are
verbatim upstream Python docstrings, taken by static extraction from
`src/garmin_mcp/*.py` at that commit. That file therefore contains copied
expression, not merely an extracted interface, and the MIT notice
requirement applies to it. No upstream code is otherwise reused: the
server is an independent Go implementation.

Licence text at the pinned commit:

```text
MIT License

Copyright (c) 2025 Alexandre Domingues

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

### `cyberjunky/python-garminconnect`

- Repository: <https://github.com/cyberjunky/python-garminconnect>
- Pinned commit: `414b54023a31259232744bb67f00a2aa71065e09` (release `0.3.10`)
- SPDX identifier: `MIT`

**Why it is recorded.** This project used the library as a protocol and
behaviour reference for Garmin login, token refresh and persistence,
endpoint shapes, payload tolerance and retry semantics. No source was
copied and no expression was reused, so no notice obligation arises from
it. The relationship is recorded because the derived behaviour is
substantial and the record should be honest about where it came from.

Licence text at the pinned commit:

```text
MIT License

Copyright (c) 2020-2026 Ron Klinkien

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

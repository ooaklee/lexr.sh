# Third-party notices

Lexr binaries are built with the projects listed below. Thank you to everyone
who maintains them. This inventory covers the modules, Go runtime, and Unicode
data linked into the six current release targets; it leaves out tools and
module-graph entries that are used only while building or testing dependencies.

Licence terms are collected after the inventory so people do not have to chase
them through a module cache. Copyright wording is preserved from the upstream
files.

## Maintenance

We aim to keep this inventory accurate as dependencies change. During active
development, it may briefly lag behind a dependency update. Before each release,
it must be reviewed against every supported binary. It must be updated before
publication. If you find an omission or outdated notice, please open an issue or
pull request.

## Components in every release target

| Component | Version | Licence | Copyright notice |
| --- | --- | --- | --- |
| [`charm.land/bubbletea/v2`](https://github.com/charmbracelet/bubbletea) | `v2.0.9` | `MIT` | `Copyright (c) 2020-2026 Charmbracelet, Inc.` |
| [`charm.land/lipgloss/v2`](https://github.com/charmbracelet/lipgloss) | `v2.0.6` | `MIT` | `Copyright (c) 2021-2026 Charmbracelet, Inc.` |
| [`github.com/charmbracelet/colorprofile`](https://github.com/charmbracelet/colorprofile) | `v0.4.3` | `MIT` | `Copyright (c) 2020-2024 Charmbracelet, Inc` |
| [`github.com/charmbracelet/ultraviolet`](https://github.com/charmbracelet/ultraviolet) | `v0.0.0-20260811164956-006e29f97886` | `MIT` | `Copyright (c) 2025 Charmbracelet, Inc` |
| [`github.com/charmbracelet/x/ansi`](https://github.com/charmbracelet/x/tree/main/ansi) | `v0.11.8` | `MIT` | `Copyright (c) 2023 Charmbracelet, Inc.` |
| [`github.com/charmbracelet/x/term`](https://github.com/charmbracelet/x/tree/main/term) | `v0.2.2` | `MIT` | `Copyright (c) 2023 Charmbracelet, Inc.` |
| [`github.com/charmbracelet/x/windows`](https://github.com/charmbracelet/x/tree/main/windows) | `v0.2.2` | `MIT` | `Copyright (c) 2023 Charmbracelet, Inc.` |
| [`github.com/clipperhouse/displaywidth`](https://github.com/clipperhouse/displaywidth) | `v0.11.0` | `MIT` | `Copyright (c) 2025 Matt Sherman` |
| [`github.com/clipperhouse/uax29/v2`](https://github.com/clipperhouse/uax29) | `v2.7.0` | `MIT` | `Copyright (c) 2020 Matt Sherman` |
| [`github.com/lucasb-eyer/go-colorful`](https://github.com/lucasb-eyer/go-colorful) | `v1.4.1` | `MIT` | `Copyright (c) 2013 Lucas Beyer` |
| [`github.com/mattn/go-runewidth`](https://github.com/mattn/go-runewidth) | `v0.0.24` | `MIT` | `Copyright (c) 2016 Yasuhiro Matsumoto` |
| [`github.com/muesli/cancelreader`](https://github.com/muesli/cancelreader) | `v0.2.2` | `MIT` | `Copyright (c) 2022 Erik Geiser and Christian Muehlhaeuser` |
| [`github.com/rivo/uniseg`](https://github.com/rivo/uniseg) | `v0.4.7` | `MIT` | `Copyright (c) 2019 Oliver Kuederle` |
| [`github.com/spf13/cobra`](https://github.com/spf13/cobra) | `v1.10.2` | `Apache-2.0` | `Copyright 2013-2023 The Cobra Authors` |
| [`github.com/spf13/pflag`](https://github.com/spf13/pflag) | `v1.0.9` | `BSD-3-Clause` | `Copyright (c) 2012 Alex Ogier. All rights reserved.`<br>`Copyright (c) 2012 The Go Authors. All rights reserved.` |
| [`github.com/xo/terminfo`](https://github.com/xo/terminfo) | `v0.0.0-20220910002029-abceb7e1c41e` | `MIT` | `Copyright (c) 2016 Anmol Sethi` |
| [`golang.org/x/sync`](https://github.com/golang/sync) | `v0.22.0` | `BSD-3-Clause` | `Copyright 2009 The Go Authors.` |
| [`golang.org/x/sys`](https://github.com/golang/sys) | `v0.47.0` | `BSD-3-Clause` | `Copyright 2009 The Go Authors.` |

## Platform-specific components

| Targets | Component | Version | Licence | Copyright notice |
| --- | --- | --- | --- | --- |
| Linux and macOS | [`github.com/charmbracelet/x/termios`](https://github.com/charmbracelet/x/tree/main/termios) | `v0.1.1` | `MIT` | `Copyright (c) 2023 Charmbracelet, Inc.` |
| Windows | [`github.com/inconshreveable/mousetrap`](https://github.com/inconshreveable/mousetrap) | `v1.1.0` | `Apache-2.0` | `Copyright 2022 Alan Shreve (@inconshreveable)` |

## Embedded Go and Unicode material

Every executable contains the [Go runtime and standard library](https://go.dev/)
under `BSD-3-Clause` terms with `Copyright 2009 The Go Authors.` The release
workflow selects the toolchain version, so this notice deliberately does not
claim a fixed Go version.

[`displaywidth`](https://github.com/clipperhouse/displaywidth),
[`uax29`](https://github.com/clipperhouse/uax29), and
[`uniseg`](https://github.com/rivo/uniseg) contain generated tables derived
from Unicode 15 and Unicode 17 data. The Unicode 15 source headers carry
`© 2022 Unicode®, Inc.`, while the Unicode 17 source headers carry
`© 2025 Unicode®, Inc.` The complete current Unicode permission notice appears
below.

## MIT terms

The following permission notice applies to every component labelled `MIT`.
The individual copyright notices in the inventory above form part of this
notice.

```text
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

## Apache 2.0 terms

The complete terms for components labelled `Apache-2.0` are provided in the
root [`LICENSE`](LICENSE), which accompanies every Lexr release. Neither Cobra
nor mousetrap supplies an upstream `NOTICE` file.

## Pflag BSD terms

The following terms apply to `github.com/spf13/pflag`.

```text
Copyright (c) 2012 Alex Ogier. All rights reserved.
Copyright (c) 2012 The Go Authors. All rights reserved.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are
met:

   * Redistributions of source code must retain the above copyright
notice, this list of conditions and the following disclaimer.
   * Redistributions in binary form must reproduce the above
copyright notice, this list of conditions and the following disclaimer
in the documentation and/or other materials provided with the
distribution.
   * Neither the name of Google Inc. nor the names of its
contributors may be used to endorse or promote products derived from
this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
"AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT
OWNER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
(INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
```

## Go BSD terms

The following terms apply to the Go runtime and standard library,
`golang.org/x/sync`, and `golang.org/x/sys`.

```text
Copyright 2009 The Go Authors.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are
met:

   * Redistributions of source code must retain the above copyright
notice, this list of conditions and the following disclaimer.
   * Redistributions in binary form must reproduce the above
copyright notice, this list of conditions and the following disclaimer
in the documentation and/or other materials provided with the
distribution.
   * Neither the name of Google LLC nor the names of its
contributors may be used to endorse or promote products derived from
this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
"AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT
OWNER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
(INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
```

The Go project also provides this additional patent grant:

```text
Additional IP Rights Grant (Patents)

"This implementation" means the copyrightable works distributed by
Google as part of the Go project.

Google hereby grants to You a perpetual, worldwide, non-exclusive,
no-charge, royalty-free, irrevocable (except as stated in this section)
patent license to make, have made, use, offer to sell, sell, import,
transfer and otherwise run, modify and propagate the contents of this
implementation of Go, where such license applies only to those patent
claims, both currently owned or controlled by Google and acquired in
the future, licensable by Google that are necessarily infringed by this
implementation of Go.  This grant does not include claims that would be
infringed only as a consequence of further modification of this
implementation.  If you or your agent or exclusive licensee institute or
order or agree to the institution of patent litigation against any
entity (including a cross-claim or counterclaim in a lawsuit) alleging
that this implementation of Go or any code incorporated within this
implementation of Go constitutes direct or contributory patent
infringement, or inducement of patent infringement, then any patent
rights granted to you under this License for this implementation of Go
shall terminate as of the date such litigation is filed.
```

## Unicode terms

The following notice applies to the generated Unicode data described above.

```text
UNICODE LICENSE V3

COPYRIGHT AND PERMISSION NOTICE

Copyright © 1991-2026 Unicode, Inc.

NOTICE TO USER: Carefully read the following legal agreement. BY
DOWNLOADING, INSTALLING, COPYING OR OTHERWISE USING DATA FILES, AND/OR
SOFTWARE, YOU UNEQUIVOCALLY ACCEPT, AND AGREE TO BE BOUND BY, ALL OF THE
TERMS AND CONDITIONS OF THIS AGREEMENT. IF YOU DO NOT AGREE, DO NOT
DOWNLOAD, INSTALL, COPY, DISTRIBUTE OR USE THE DATA FILES OR SOFTWARE.

Permission is hereby granted, free of charge, to any person obtaining a
copy of data files and any associated documentation (the "Data Files") or
software and any associated documentation (the "Software") to deal in the
Data Files or Software without restriction, including without limitation
the rights to use, copy, modify, merge, publish, distribute, and/or sell
copies of the Data Files or Software, and to permit persons to whom the
Data Files or Software are furnished to do so, provided that either (a)
this copyright and permission notice appear with all copies of the Data
Files or Software, or (b) this copyright and permission notice appear in
associated Documentation.

THE DATA FILES AND SOFTWARE ARE PROVIDED "AS IS", WITHOUT WARRANTY OF ANY
KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF
MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT OF
THIRD PARTY RIGHTS.

IN NO EVENT SHALL THE COPYRIGHT HOLDER OR HOLDERS INCLUDED IN THIS NOTICE
BE LIABLE FOR ANY CLAIM, OR ANY SPECIAL INDIRECT OR CONSEQUENTIAL DAMAGES,
OR ANY DAMAGES WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS,
WHETHER IN AN ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION,
ARISING OUT OF OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THE DATA
FILES OR SOFTWARE.

Except as contained in this notice, the name of a copyright holder shall
not be used in advertising or otherwise to promote the sale, use or other
dealings in these Data Files or Software without prior written
authorization of the copyright holder.
```

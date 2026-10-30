# Third-party notices and corresponding source

This file contains the upstream **Tencent WeKnora (MIT)** license notice and the
third-party open-source notices that accompany the root GENYEE Commercial
[`LICENSE`](LICENSE). The GENYEE Commercial License governs GENYEE's original
code, modifications, and product compilation as a whole, and does not replace or
restrict the open-source licenses of upstream Tencent WeKnora or any third-party
components listed here. Keep this file, `LICENSE`, and the `licenses/` directory
with all redistributed backend, frontend, container, and desktop packages.
`scripts/copy-licenses.sh` adds checksum-verified source archives at packaging
time; they are not stored in this Git repository. Container distributions
include the notice bundle in `/app`, `/licenses`, or `/usr/share/licenses/genyee/`;
macOS applications include them in `Contents/Resources`; Windows installers
place them next to the installed executable.

## Upstream Foundation — Tencent WeKnora (MIT)

GENYEE is built upon the open-source **Tencent WeKnora** project
([`https://github.com/Tencent/WeKnora`](https://github.com/Tencent/WeKnora)),
upstream base commit `f17ee8a8d` (`v0.9.0`). All upstream WeKnora code and
Tencent modifications remain licensed under the MIT License and the respective
third-party licenses stated in the upstream `LICENSE` file, reproduced verbatim
below (283 lines):

```text
Tencent is pleased to support the open source community by making  WeKnora available. 

Copyright (C) 2026 Tencent.  All rights reserved. 

The open-source software included in this distribution may have been modified by Tencent (“Tencent Modifications”). All Tencent Modifications are Copyright (C) Tencent. 

 WeKnora is licensed under the MIT except for the third-party components listed below.


Terms of the MIT:
--------------------------------------------------------------------
Permission is hereby granted, free of charge, to any person obtaining
a copy of this software and associated documentation files (the
"Software"), to deal in the Software without restriction, including
without limitation the rights to use, copy, modify, merge, publish,
distribute, sublicense, and/or sell copies of the Software, and to
permit persons to whom the Software is furnished to do so, subject to
the following conditions:

The above copyright notice and this permission notice shall be
included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND,
EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF
MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT.
IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY
CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT,
TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE
SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.


Dependencies and Licenses:

This open-source project,  WeKnora, builds upon the following open-source software components, each of which remains licensed under its original license. Certain software may include modifications made by Tencent (“Tencent Modifications”), which are Copyright (C) Tencent.

In case you believe there have been errors in the attribution below, you may submit the concerns to us for review and correction.


Open Source Software Licensed under the Apache-2.0
--------------------------------------------------------------------
1. github.com/longbridgeapp/opencc
Copyright (c) github.com/longbridgeapp/opencc authors.
You may obtain the source code and detailed information about this component at https://pkg.go.dev/github.com/longbridgeapp/opencc.



Terms of the Apache-2.0
--------------------------------------------------------------------
Apache License
                           Version 2.0, January 2004
                        http://www.apache.org/licenses/

   TERMS AND CONDITIONS FOR USE, REPRODUCTION, AND DISTRIBUTION

   1. Definitions.

      "License" shall mean the terms and conditions for use, reproduction,
      and distribution as defined by Sections 1 through 9 of this document.

      "Licensor" shall mean the copyright owner or entity authorized by
      the copyright owner that is granting the License.

      "Legal Entity" shall mean the union of the acting entity and all
      other entities that control, are controlled by, or are under common
      control with that entity. For the purposes of this definition,
      "control" means (i) the power, direct or indirect, to cause the
      direction or management of such entity, whether by contract or
      otherwise, or (ii) ownership of fifty percent (50%) or more of the
      outstanding shares, or (iii) beneficial ownership of such entity.

      "You" (or "Your") shall mean an individual or Legal Entity
      exercising permissions granted by this License.

      "Source" form shall mean the preferred form for making modifications,
      including but not limited to software source code, documentation
      source, and configuration files.

      "Object" form shall mean any form resulting from mechanical
      transformation or translation of a Source form, including but
      not limited to compiled object code, generated documentation,
      and conversions to other media types.

      "Work" shall mean the work of authorship, whether in Source or
      Object form, made available under the License, as indicated by a
      copyright notice that is included in or attached to the work
      (an example is provided in the Appendix below).

      "Derivative Works" shall mean any work, whether in Source or Object
      form, that is based on (or derived from) the Work and for which the
      editorial revisions, annotations, elaborations, or other modifications
      represent, as a whole, an original work of authorship. For the purposes
      of this License, Derivative Works shall not include works that remain
      separable from, or merely link (or bind by name) to the interfaces of,
      the Work and Derivative Works thereof.

      "Contribution" shall mean any work of authorship, including
      the original version of the Work and any modifications or additions
      to that Work or Derivative Works thereof, that is intentionally
      submitted to Licensor for inclusion in the Work by the copyright owner
      or by an individual or Legal Entity authorized to submit on behalf of
      the copyright owner. For the purposes of this definition, "submitted"
      means any form of electronic, verbal, or written communication sent
      to the Licensor or its representatives, including but not limited to
      communication on electronic mailing lists, source code control systems,
      and issue tracking systems that are managed by, or on behalf of, the
      Licensor for the purpose of discussing and improving the Work, but
      excluding communication that is conspicuously marked or otherwise
      designated in writing by the copyright owner as "Not a Contribution."

      "Contributor" shall mean Licensor and any individual or Legal Entity
      on behalf of whom a Contribution has been received by Licensor and
      subsequently incorporated within the Work.

   2. Grant of Copyright License. Subject to the terms and conditions of
      this License, each Contributor hereby grants to You a perpetual,
      worldwide, non-exclusive, no-charge, royalty-free, irrevocable
      copyright license to reproduce, prepare Derivative Works of,
      publicly display, publicly perform, sublicense, and distribute the
      Work and such Derivative Works in Source or Object form.

   3. Grant of Patent License. Subject to the terms and conditions of
      this License, each Contributor hereby grants to You a perpetual,
      worldwide, non-exclusive, no-charge, royalty-free, irrevocable
      (except as stated in this section) patent license to make, have made,
      use, offer to sell, sell, import, and otherwise transfer the Work,
      where such license applies only to those patent claims licensable
      by such Contributor that are necessarily infringed by their
      Contribution(s) alone or by combination of their Contribution(s)
      with the Work to which such Contribution(s) was submitted. If You
      institute patent litigation against any entity (including a
      cross-claim or counterclaim in a lawsuit) alleging that the Work
      or a Contribution incorporated within the Work constitutes direct
      or contributory patent infringement, then any patent licenses
      granted to You under this License for that Work shall terminate
      as of the date such litigation is filed.

   4. Redistribution. You may reproduce and distribute copies of the
      Work or Derivative Works thereof in any medium, with or without
      modifications, and in Source or Object form, provided that You
      meet the following conditions:

      (a) You must give any other recipients of the Work or
          Derivative Works a copy of this License; and

      (b) You must cause any modified files to carry prominent notices
          stating that You changed the files; and

      (c) You must retain, in the Source form of any Derivative Works
          that You distribute, all copyright, patent, trademark, and
          attribution notices from the Source form of the Work,
          excluding those notices that do not pertain to any part of
          the Derivative Works; and

      (d) If the Work includes a "NOTICE" text file as part of its
          distribution, then any Derivative Works that You distribute must
          include a readable copy of the attribution notices contained
          within such NOTICE file, excluding those notices that do not
          pertain to any part of the Derivative Works, in at least one
          of the following places: within a NOTICE text file distributed
          as part of the Derivative Works; within the Source form or
          documentation, if provided along with the Derivative Works; or,
          within a display generated by the Derivative Works, if and
          wherever such third-party notices normally appear. The contents
          of the NOTICE file are for informational purposes only and
          do not modify the License. You may add Your own attribution
          notices within Derivative Works that You distribute, alongside
          or as an addendum to the NOTICE text from the Work, provided
          that such additional attribution notices cannot be construed
          as modifying the License.

      You may add Your own copyright statement to Your modifications and
      may provide additional or different license terms and conditions
      for use, reproduction, or distribution of Your modifications, or
      for any such Derivative Works as a whole, provided Your use,
      reproduction, and distribution of the Work otherwise complies with
      the conditions stated in this License.

   5. Submission of Contributions. Unless You explicitly state otherwise,
      any Contribution intentionally submitted for inclusion in the Work
      by You to the Licensor shall be under the terms and conditions of
      this License, without any additional terms or conditions.
      Notwithstanding the above, nothing herein shall supersede or modify
      the terms of any separate license agreement you may have executed
      with Licensor regarding such Contributions.

   6. Trademarks. This License does not grant permission to use the trade
      names, trademarks, service marks, or product names of the Licensor,
      except as required for reasonable and customary use in describing the
      origin of the Work and reproducing the content of the NOTICE file.

   7. Disclaimer of Warranty. Unless required by applicable law or
      agreed to in writing, Licensor provides the Work (and each
      Contributor provides its Contributions) on an "AS IS" BASIS,
      WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
      implied, including, without limitation, any warranties or conditions
      of TITLE, NON-INFRINGEMENT, MERCHANTABILITY, or FITNESS FOR A
      PARTICULAR PURPOSE. You are solely responsible for determining the
      appropriateness of using or redistributing the Work and assume any
      risks associated with Your exercise of permissions under this License.

   8. Limitation of Liability. In no event and under no legal theory,
      whether in tort (including negligence), contract, or otherwise,
      unless required by applicable law (such as deliberate and grossly
      negligent acts) or agreed to in writing, shall any Contributor be
      liable to You for damages, including any direct, indirect, special,
      incidental, or consequential damages of any character arising as a
      result of this License or out of the use or inability to use the
      Work (including but not limited to damages for loss of goodwill,
      work stoppage, computer failure or malfunction, or any and all
      other commercial damages or losses), even if such Contributor
      has been advised of the possibility of such damages.

   9. Accepting Warranty or Additional Liability. While redistributing
      the Work or Derivative Works thereof, You may choose to offer,
      and charge a fee for, acceptance of support, warranty, indemnity,
      or other liability obligations and/or rights consistent with this
      License. However, in accepting such obligations, You may act only
      on Your own behalf and on Your sole responsibility, not on behalf
      of any other Contributor, and only if You agree to indemnify,
      defend, and hold each Contributor harmless for any liability
      incurred by, or claims asserted against, such Contributor by reason
      of your accepting any such warranty or additional liability.

   END OF TERMS AND CONDITIONS

   APPENDIX: How to apply the Apache License to your work.

      To apply the Apache License to your work, attach the following
      boilerplate notice, with the fields enclosed by brackets "[]"
      replaced with your own identifying information. (Don't include
      the brackets!)  The text should be enclosed in the appropriate
      comment syntax for the file format. We also recommend that a
      file or class name and description of purpose be included on the
      same "printed page" as the copyright notice for easier
      identification within third-party archives.

   Copyright [yyyy] [name of copyright owner]

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.


Open Source Software Licensed under the MIT
--------------------------------------------------------------------
1. anydoc
Copyright (c) 2026 Sideguide Technologies Inc.
Please note this software has been modified by Tencent in this distribution.

2. github.com/wailsapp/wails/v2
Copyright (c) github.com/wailsapp/wails/v2 authors.
You may obtain the source code and detailed information about this component at https://pkg.go.dev/github.com/wailsApp/wails/v2.



Terms of the MIT
--------------------------------------------------------------------
Permission is hereby granted, free of charge, to any person obtaining
a copy of this software and associated documentation files (the
"Software"), to deal in the Software without restriction, including
without limitation the rights to use, copy, modify, merge, publish,
distribute, sublicense, and/or sell copies of the Software, and to
permit persons to whom the Software is furnished to do so, subject to
the following conditions:

The above copyright notice and this permission notice shall be
included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND,
EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF
MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT.
IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY
CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT,
TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE
SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
```

## BrowserSkill

- Component: `BrowserSkill` ([`https://github.com/Tencent/BrowserSkill`](https://github.com/Tencent/BrowserSkill)).
- Files: `frontend/src/assets/browserskill/` (including `frontend/src/assets/browserskill/LICENSE` and `website-docs/homepage/public/docs/_home/brands/BROWSERSKILL-LICENSE`).
- License: MIT License.
- Copyright: `Copyright (c) 2026 Tencent`.
- Notice: Kept intact in `frontend/src/assets/browserskill/LICENSE` and `website-docs/homepage/public/docs/_home/brands/BROWSERSKILL-LICENSE` under the MIT terms reproduced above.

## mcp-server

- Component: `mcp-server` Python MCP server package (`mcp-server/`).
- License: MIT License, preserved in [`mcp-server/LICENSE`](mcp-server/LICENSE).
- Copyright: `Copyright (c) 2024 WeKnora Team`.
- Notice: Kept intact in `mcp-server/LICENSE` and `/licenses/LICENSE` inside the `mcp-server` container image under the MIT terms reproduced above.

## Go MySQL Driver

- Component: `github.com/go-sql-driver/mysql`, version `v1.10.0`.
- Used in backend and desktop binaries, including the Doris MySQL protocol driver.
- License: Mozilla Public License 2.0, reproduced in
  [`licenses/go-sql-driver-mysql-MPL-2.0.txt`](licenses/go-sql-driver-mysql-MPL-2.0.txt).
- Copyright: The Go-MySQL-Driver Authors; the original per-file notices and
  `AUTHORS` are preserved in the accompanying source archive.
- Modifications by WeKnora / GENYEE: none. Dialer configuration is in separate application files.
- Corresponding source, available under MPL-2.0, is included in binary releases
  as `licenses/sources/mysql-v1.10.0.zip`. Source repository users can obtain it from the
  [Go module proxy](https://proxy.golang.org/github.com/go-sql-driver/mysql/@v/v1.10.0.zip)
  and [upstream repository](https://github.com/go-sql-driver/mysql/tree/v1.10.0).

## go-m1cpu

- Component: `github.com/shoenig/go-m1cpu`, version `v0.1.6`.
- Used by the macOS backend/desktop dependency chain through gopsutil. It is
  absent from Linux backend build dependencies. Its source is included in all
  notice bundles for consistent packaging; this does not imply Linux linkage.
- License: Mozilla Public License 2.0, reproduced in
  [`licenses/go-m1cpu-MPL-2.0.txt`](licenses/go-m1cpu-MPL-2.0.txt).
- Attribution: the go-m1cpu project and its contributors. The complete original
  source and notices are preserved in the accompanying archive.
- Modifications by WeKnora / GENYEE: none.
- Corresponding source, available under MPL-2.0, is included in binary releases
  as `licenses/sources/go-m1cpu-v0.1.6.zip`. Source repository users can obtain it from the
  [Go module proxy](https://proxy.golang.org/github.com/shoenig/go-m1cpu/@v/v0.1.6.zip)
  and [upstream repository](https://github.com/shoenig/go-m1cpu/tree/v0.1.6).

## OpenCC dictionary data

- Files: `internal/textconv/data/TSPhrases.txt` and `TSCharacters.txt`, copied
  unchanged from `github.com/longbridgeapp/opencc` version `v0.3.13`.
- Attribution: the OpenCC and longbridge/opencc contributors.
- License: Apache-2.0, reproduced in
  [`licenses/OpenCC-Apache-2.0.txt`](licenses/OpenCC-Apache-2.0.txt).
- Source: [versioned dictionary directory](https://github.com/longbridgeapp/opencc/tree/v0.3.13/dictionary)
  and the two text files distributed with the source tree.
- Only dictionary data is retained. The application uses its own standard-library lookup
  implementation; the upstream Go converter, `liuzl/da`, and GPL-licensed
  `cedar-go` code are not included.

## go-ego/gse

- Component: `github.com/go-ego/gse`, version `v0.80.3`.
- Used for Chinese and multilingual text segmentation in the Go backend.
- License: Apache License 2.0 (see the Apache-2.0 terms reproduced above).
- Copyright: `Copyright (c) go-ego/gse authors`.
- Modifications by WeKnora / GENYEE: none.
- Corresponding source: [Go module proxy](https://proxy.golang.org/github.com/go-ego/gse/@v/v0.80.3.zip)
  and [upstream repository](https://github.com/go-ego/gse/tree/v0.80.3).

## vcaesar/cedar

- Component: `github.com/vcaesar/cedar`, version `v0.20.2`.
- Used as a double-array trie library by `github.com/go-ego/gse` and the Tencent
  VectorDB SDK (`github.com/tencent/vectordatabase-sdk-go/tcvdbtext/tokenizer`);
  this is a permissive BSD-2-Clause module distinct from the GPL-licensed
  `cedar-go` package.
- License: BSD 2-Clause License.
- Copyright: `Copyright (c) vcaesar/cedar authors`.
- Modifications by WeKnora / GENYEE: none.
- Corresponding source: [Go module proxy](https://proxy.golang.org/github.com/vcaesar/cedar/@v/v0.20.2.zip)
  and [upstream repository](https://github.com/vcaesar/cedar/tree/v0.20.2).

## Build-only component

The Windows installer template in `cmd/desktop/build/windows/installer/project.nsi`
is based on Wails v2.12.0's MIT-licensed default template, with local changes to
install this notice/source bundle. Its license and copyright are reproduced in
[`licenses/Wails-MIT.txt`](licenses/Wails-MIT.txt).

`cbindgen` 0.29.4 (MPL-2.0) generates the AnyDoc C ABI header during the Rust
build. Its code/tool executable is not copied into the runtime image or release
packages by the current build recipes. Generating the header does not copy the
tool's implementation into the product. If distributing build environments or
the tool itself, retain its license and provide its corresponding source too.

## Maintaining this bundle

When upgrading either MPL module, update the version, license, and SHA-256 pin in
`licenses/sources/modules.tsv` together. Archives are unmodified Go module proxy
ZIPs, including their original copyright notices. Verify them with
`go mod download -json` and the module's `go.sum` entry before updating.
`scripts/check-license-bundle.sh` checks the pins and notices without downloading
dependencies. `scripts/copy-licenses.sh DESTINATION` obtains the pinned archives
through Go's configured `GOPROXY` and module cache, verifies their SHA-256 hashes,
and includes them in the destination bundle. Offline packaging requires those
module archives to be present in the Go module cache beforehand. To verify a
packaged source directory, run `scripts/check-license-bundle.sh PATH/TO/licenses/sources`.

## Docreader OCR page-preprocessing components (Python)

The docreader service straightens and crops photographed pages before OCR. That
capability adds the following Python components to the docreader image. They are
redistributed inside the container image, so their notices travel here rather
than in `licenses/sources/modules.tsv`, which records Go modules only
(`scripts/check-license-bundle.sh` verifies that manifest against `go.mod`).

### RapidOCR

- Component: `rapidocr`, version `3.10.0` (Python package, docreader service).
- Used in the docreader service to detect text lines and score their
  recognition, which is what the page-cropping pipeline keys off.
- License: Apache License 2.0, reproduced in
  [`licenses/rapidocr-Apache-2.0.txt`](licenses/rapidocr-Apache-2.0.txt).
- Copyright: The RapidOCR Authors.
- Model artifacts: RapidOCR redistributes OCR model weights whose copyright is
  held separately by Baidu and/or the applicable PaddleOCR rights holders. They
  are distributed under Apache-2.0 as well, and their provenance and
  redistribution terms are recorded in
  [`licenses/rapidocr-MODEL_LICENSES.md`](licenses/rapidocr-MODEL_LICENSES.md).
- Modifications: none. The integration calls the published API and
  adds no changes to the component.
- Corresponding source: [PyPI](https://pypi.org/project/rapidocr/3.10.0/) and
  [upstream repository](https://github.com/RapidAI/RapidOCR).

### ONNX Runtime

- Component: `onnxruntime`, version `1.23.2` (Python package, docreader service).
- Used as the inference engine RapidOCR runs its models on.
- License: MIT License, reproduced in
  [`licenses/onnxruntime-MIT.txt`](licenses/onnxruntime-MIT.txt).
- Copyright: Microsoft Corporation.
- Modifications: none.
- Corresponding source: [PyPI](https://pypi.org/project/onnxruntime/1.23.2/) and
  [upstream repository](https://github.com/microsoft/onnxruntime).

### OpenCV (headless)

- Component: `opencv-python-headless`, version `5.0.0.93` (Python package,
  docreader service).
- Used for geometric image operations: rotation with canvas expansion, line
  intersection, homography and warping.
- License: Apache License 2.0, reproduced in
  [`licenses/opencv-python-headless-Apache-2.0.txt`](licenses/opencv-python-headless-Apache-2.0.txt);
  bundled third-party components are listed in
  [`licenses/opencv-python-headless-THIRD-PARTY.txt`](licenses/opencv-python-headless-THIRD-PARTY.txt).
- Copyright: The OpenCV Foundation and contributors (see the notice file for
  the per-component attributions it carries).
- Modifications: none.
- Corresponding source: [PyPI](https://pypi.org/project/opencv-python-headless/5.0.0.93/)
  and [upstream repository](https://github.com/opencv/opencv-python).

### NumPy

- Component: `numpy`, version `2.3.4` (Python package, docreader service).
- Used for array manipulation throughout the page-preprocessing pipeline.
- License: BSD 3-Clause License, reproduced in
  [`licenses/numpy-BSD-3-Clause.txt`](licenses/numpy-BSD-3-Clause.txt).
- Copyright: The NumPy Developers.
- Modifications: none.
- Corresponding source: [PyPI](https://pypi.org/project/numpy/2.3.4/) and
  [upstream repository](https://github.com/numpy/numpy).

## Transitive Weak-Copyleft and Dual-License Elections (Python & Node.js)

The following transitive dependencies in the Python (`docreader`, `mcp-server`)
and Node.js (`frontend`) environments carry weak-copyleft or multi-license
terms. None of these packages are modified by GENYEE:

- **`certifi`** (Python, `docreader` / `mcp-server`): Licensed under the
  Mozilla Public License 2.0 (`MPL-2.0`). Distributed unmodified from PyPI;
  corresponding source is available at
  [https://github.com/certifi/python-certifi](https://github.com/certifi/python-certifi).
- **`tqdm`** (Python, `docreader`): Licensed under `MIT` and `MPL-2.0`. GENYEE
  elects the **MIT License** for all dual-licensed portions and retains the
  MPL-2.0 notices for MPL-covered files; corresponding source is available at
  [https://github.com/tqdm/tqdm](https://github.com/tqdm/tqdm).
- **`tld`** (Python, `docreader`, version `0.13.1`): Tri-licensed under
  `MPL-1.1 OR GPL-2.0-only OR LGPL-2.1-only`. GENYEE elects the **Mozilla Public
  License 1.1 (`MPL-1.1`)**. Distributed unmodified; corresponding source is
  available at [https://github.com/barseghyanartur/tld](https://github.com/barseghyanartur/tld).
- **`@novnc/novnc`** (Node.js, `frontend` sandbox desktop VNC client): Licensed
  under the Mozilla Public License 2.0 (`MPL-2.0`). Distributed unmodified;
  corresponding source is available at
  [https://github.com/novnc/noVNC](https://github.com/novnc/noVNC).
- **`jszip`** (Node.js, `frontend`): Dual-licensed under `MIT OR GPL-3.0-or-later`.
  GENYEE elects the **MIT License**; corresponding source is available at
  [https://github.com/Stuk/jszip](https://github.com/Stuk/jszip).

## Written Offer for Corresponding Source (Service Containers & System Packages)

Certain deployment topologies and offline installation bundles include
unmodified third-party service container images and standard Linux base-image
system packages that are licensed under the GNU General Public License (GPL),
GNU Lesser General Public License (LGPL), GNU Affero General Public License
(AGPL), or Mozilla Public License (MPL):

1. **Unmodified Third-Party Service Containers** (run as separate, isolated
   services communicating over standard network protocols):
   - **ParadeDB** (`paradedb/paradedb` — `pg_search` extension under `AGPL-3.0`,
     PostgreSQL and `pgvector` under the PostgreSQL License):
     [https://github.com/paradedb/paradedb](https://github.com/paradedb/paradedb)
   - **MinIO** (`pgsty/minio` / MinIO object storage server under `AGPL-3.0`):
     [https://github.com/minio/minio](https://github.com/minio/minio)
   - **SearXNG** (`searxng/searxng` metasearch service under `AGPL-3.0`):
     [https://github.com/searxng/searxng](https://github.com/searxng/searxng)
   - **Neo4j Community Edition** (`neo4j` graph database service under `GPL-3.0`):
     [https://github.com/neo4j/neo4j](https://github.com/neo4j/neo4j)

2. **Debian and Alpine System Packages in GENYEE Container Images**:
   GENYEE-built container images (`app`, `docreader`, `sandbox`, `odl-hybrid`,
   `ui`, `mcp-server`) are built on official Debian (`bookworm` / `slim`) and
   Alpine Linux base images and include unmodified system libraries and
   command-line utilities invoked as separate subprocesses (including `glibc`
   under LGPL-2.1+, `libstdc++`/`libgcc` under GPL-3.0 with the GCC Runtime
   Library Exception, `bash` and `coreutils` under GPL-3.0+, `antiword` under
   GPL-2.0, `libreoffice` under MPL-2.0, `openjdk-17-jre-headless` under
   GPL-2.0 with the Classpath Exception, `ffmpeg` under GPL/LGPL, and Playwright
   WebKit runtime libraries under LGPL/BSD).

**Written Offer (Valid for 3 Years):**
To the extent any GENYEE binary or container distribution includes software
governed by the GPL, LGPL, AGPL, or MPL whose Corresponding Source is not
already included in `licenses/sources/`, GENYEE hereby offers, for a period of
**three (3) years** from the date you received the distribution, to provide a
complete machine-readable copy of the Corresponding Source (including the exact
upstream source archives and build scripts for the unmodified service images and
Debian/Alpine packages) on a durable physical medium or via electronic download
at no charge beyond the cost of physically performing the source distribution.
To request Corresponding Source under this offer, please contact GENYEE support
through the support channel specified in your commercial license agreement or
at [`https://github.com/Usir233/GENYEE/issues`](https://github.com/Usir233/GENYEE/issues).

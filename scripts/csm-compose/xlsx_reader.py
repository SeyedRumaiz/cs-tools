#!/usr/bin/env python3
# Copyright (c) 2026 WSO2 LLC. (https://www.wso2.com).
#
# WSO2 LLC. licenses this file to you under the Apache License,
# Version 2.0 (the "License"); you may not use this file except
# in compliance with the License.
# You may obtain a copy of the License at
#
# http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing,
# software distributed under the License is distributed on an
# "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
# KIND, either express or implied.  See the License for the
# specific language governing permissions and limitations
# under the License.

"""
A minimal .xlsx reader for the local-dev rota importers: cell values and cell
fill colours, stdlib only. It reads the file as the zip of XML it is, because
the importers need colours as well as values -- the CRE sheet carries some of
its meaning in fill colour alone, which a CSV export loses.
"""

import re
import zipfile
import xml.etree.ElementTree as ET

M = "{http://schemas.openxmlformats.org/spreadsheetml/2006/main}"
R = "{http://schemas.openxmlformats.org/officeDocument/2006/relationships}"


class Book:
    def __init__(self, path):
        self.z = zipfile.ZipFile(path)
        wb = ET.fromstring(self.z.read("xl/workbook.xml"))
        rels = {r.get("Id"): r.get("Target") for r in ET.fromstring(self.z.read("xl/_rels/workbook.xml.rels"))}
        self.sheets = {}
        for s in wb.find(M + "sheets"):
            t = rels[s.get(R + "id")].lstrip("/")
            self.sheets[s.get("name")] = t if t.startswith("xl/") else "xl/" + t
        self.shared = []
        if "xl/sharedStrings.xml" in self.z.namelist():
            for si in ET.fromstring(self.z.read("xl/sharedStrings.xml")):
                self.shared.append("".join(t.text or "" for t in si.iter(M + "t")))
        st = ET.fromstring(self.z.read("xl/styles.xml"))
        fills = []
        for f in st.find(M + "fills"):
            pf = f.find(M + "patternFill")
            fg = pf.find(M + "fgColor") if pf is not None else None
            fills.append(fg.get("rgb") if fg is not None else None)
        self.xf_fill = [fills[int(x.get("fillId", 0))] for x in st.find(M + "cellXfs")]

    def grid(self, name):
        """{(row, col): (value, fill)}, 1-based."""
        root = ET.fromstring(self.z.read(self.sheets[name]))
        out = {}
        for c in root.iter(M + "c"):
            m = re.match(r"([A-Z]+)(\d+)", c.get("r"))
            col = 0
            for ch in m.group(1):
                col = col * 26 + ord(ch) - 64
            t, v = c.get("t"), c.find(M + "v")
            val = None
            if t == "s" and v is not None:
                val = self.shared[int(v.text)]
            elif t == "inlineStr":
                val = "".join(x.text or "" for x in c.iter(M + "t"))
            elif v is not None:
                val = v.text
            fill = self.xf_fill[int(c.get("s"))] if c.get("s") else None
            val = val.strip() if isinstance(val, str) else val
            if val or fill:
                out[(int(m.group(2)), col)] = (val or None, fill)
        return out

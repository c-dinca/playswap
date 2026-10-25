"""Writes a Windows resource object (.syso) that the Go linker embeds:
icon group 1, a manifest and version info. Usage:

  python3 mkres.py out.syso icon.ico "Product" "Description" 1.2.3 OriginalName.exe
"""
import struct
import sys

RT_ICON, RT_GROUP_ICON, RT_VERSION, RT_MANIFEST = 3, 14, 16, 24
LANG = 0x0409

MANIFEST = """<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0" xmlns:asmv3="urn:schemas-microsoft-com:asm.v3">
  <assemblyIdentity type="win32" name="PlaySwap.{name}" version="{ver4}" processorArchitecture="amd64"/>
  <description>{desc}</description>
  <compatibility xmlns="urn:schemas-microsoft-com:compatibility.v1">
    <application>
      <supportedOS Id="{{8e0f7a12-bfb3-4fe8-b9a5-48fd50a15a9a}}"/>
    </application>
  </compatibility>
  <trustInfo xmlns="urn:schemas-microsoft-com:asm.v3">
    <security>
      <requestedPrivileges>
        <requestedExecutionLevel level="asInvoker" uiAccess="false"/>
      </requestedPrivileges>
    </security>
  </trustInfo>
  <asmv3:application>
    <asmv3:windowsSettings>
      <dpiAware xmlns="http://schemas.microsoft.com/SMI/2005/WindowsSettings">true/pm</dpiAware>
      <dpiAwareness xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">PerMonitorV2</dpiAwareness>
      <longPathAware xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">true</longPathAware>
    </asmv3:windowsSettings>
  </asmv3:application>
  <dependency>
    <dependentAssembly>
      <assemblyIdentity type="win32" name="Microsoft.Windows.Common-Controls" version="6.0.0.0" processorArchitecture="*" publicKeyToken="6595b64144ccf1df" language="*"/>
    </dependentAssembly>
  </dependency>
</assembly>
"""


def pad4(b):
    return b + b"\0" * (-len(b) % 4)


def ver_node(key, value=b"", wtype=1, children=(), value_len=None):
    k = (key + "\0").encode("utf-16-le")
    head_len = 6 + len(k)
    body = bytearray()
    body += k
    # value starts 4-byte aligned relative to the node start
    while (6 + len(body)) % 4:
        body += b"\0"
    body += value
    for c in children:
        while (6 + len(body)) % 4:
            body += b"\0"
        body += c
    if value_len is None:
        value_len = len(value)
    return struct.pack("<HHH", 6 + len(body), value_len, wtype) + bytes(body)


def version_info(product, desc, version, original):
    parts = [int(x) for x in (version.split(".") + ["0"] * 4)[:4]]
    ms = (parts[0] << 16) | parts[1]
    ls = (parts[2] << 16) | parts[3]
    fixed = struct.pack("<13I", 0xFEEF04BD, 0x00010000, ms, ls, ms, ls, 0x3F, 0, 0x40004, 1, 0, 0, 0)
    strings = [
        ("CompanyName", "PlaySwap"),
        ("FileDescription", desc),
        ("FileVersion", version),
        ("InternalName", original.rsplit(".", 1)[0]),
        ("LegalCopyright", "PlaySwap"),
        ("OriginalFilename", original),
        ("ProductName", product),
        ("ProductVersion", version),
    ]
    str_nodes = []
    for k, v in strings:
        val = (v + "\0").encode("utf-16-le")
        str_nodes.append(ver_node(k, val, 1, (), value_len=len(v) + 1))
    table = ver_node("040904B0", b"", 1, str_nodes, 0)
    sfi = ver_node("StringFileInfo", b"", 1, [table], 0)
    var = ver_node("Translation", struct.pack("<HH", LANG, 1200), 0, (), 4)
    vfi = ver_node("VarFileInfo", b"", 1, [var], 0)
    return ver_node("VS_VERSION_INFO", fixed, 0, [sfi, vfi], 52)


def read_ico(path):
    data = open(path, "rb").read()
    _, typ, count = struct.unpack_from("<HHH", data, 0)
    assert typ == 1, "not an .ico"
    images, group = [], bytearray(struct.pack("<HHH", 0, 1, count))
    for i in range(count):
        w, h, cc, res, planes, bits, size, off = struct.unpack_from("<BBBBHHII", data, 6 + 16 * i)
        images.append(data[off:off + size])
        group += struct.pack("<BBBBHHIH", w, h, cc, res, planes, bits, size, i + 1)
    return images, bytes(group)


def build(resources):
    """resources: list of (type, id, lang, bytes). Returns (section bytes, reloc offsets)."""
    tree = {}
    for t, n, l, d in resources:
        tree.setdefault(t, {}).setdefault(n, {})[l] = d
    types = sorted(tree)

    def dir_size(n):
        return 16 + 8 * n

    # layout: root, type dirs, name dirs, data entries, blobs
    off = dir_size(len(types))
    type_off, name_off = {}, {}
    for t in types:
        type_off[t] = off
        off += dir_size(len(tree[t]))
    for t in types:
        for n in sorted(tree[t]):
            name_off[(t, n)] = off
            off += dir_size(len(tree[t][n]))
    entry_off = {}
    for t in types:
        for n in sorted(tree[t]):
            for l in sorted(tree[t][n]):
                entry_off[(t, n, l)] = off
                off += 16
    blob_off = {}
    for t in types:
        for n in sorted(tree[t]):
            for l in sorted(tree[t][n]):
                off += -off % 8
                blob_off[(t, n, l)] = off
                off += len(tree[t][n][l])
    sec = bytearray(off + (-off % 8))

    def put_dir(at, ids, target):
        struct.pack_into("<IIHHHH", sec, at, 0, 0, 0, 0, 0, len(ids))
        for i, (id_, to, is_dir) in enumerate(target(ids)):
            struct.pack_into("<II", sec, at + 16 + 8 * i, id_, to | (0x80000000 if is_dir else 0))

    put_dir(0, types, lambda ids: [(t, type_off[t], True) for t in ids])
    for t in types:
        put_dir(type_off[t], sorted(tree[t]), lambda ids, t=t: [(n, name_off[(t, n)], True) for n in ids])
        for n in sorted(tree[t]):
            put_dir(name_off[(t, n)], sorted(tree[t][n]), lambda ids, t=t, n=n: [(l, entry_off[(t, n, l)], False) for l in ids])
    relocs = []
    for key, eo in entry_off.items():
        t, n, l = key
        d = tree[t][n][l]
        struct.pack_into("<IIII", sec, eo, blob_off[key], len(d), 0, 0)
        relocs.append(eo)
        sec[blob_off[key]:blob_off[key] + len(d)] = d
    return bytes(sec), sorted(relocs)


def coff(section, relocs):
    raw_ptr = 20 + 40
    reloc_ptr = raw_ptr + len(section)
    sym_ptr = reloc_ptr + 10 * len(relocs)
    out = bytearray()
    out += struct.pack("<HHIIIHH", 0x8664, 1, 0, sym_ptr, 1, 0, 0)
    out += struct.pack("<8sIIIIIIHHI", b".rsrc", 0, 0, len(section), raw_ptr, reloc_ptr, 0, len(relocs), 0, 0x40000040)
    out += section
    for r in relocs:
        out += struct.pack("<IIH", r, 0, 3)  # IMAGE_REL_AMD64_ADDR32NB against symbol 0
    out += struct.pack("<8sIhHBB", b".rsrc", 0, 1, 0, 3, 0)
    out += struct.pack("<I", 4)
    return bytes(out)


def main():
    out, ico, product, desc, version, original = sys.argv[1:7]
    images, group = read_ico(ico)
    ver4 = ".".join((version.split(".") + ["0"] * 4)[:4])
    res = [(RT_ICON, i + 1, LANG, img) for i, img in enumerate(images)]
    res.append((RT_GROUP_ICON, 1, LANG, group))
    res.append((RT_MANIFEST, 1, LANG, MANIFEST.format(name=original.rsplit(".", 1)[0], ver4=ver4, desc=desc).encode("utf-8")))
    res.append((RT_VERSION, 1, LANG, version_info(product, desc, version, original)))
    sec, relocs = build(res)
    open(out, "wb").write(coff(sec, relocs))


if __name__ == "__main__":
    main()

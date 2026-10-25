"""Sanity-checks a built exe: GUI subsystem, resource tree, icon group, manifest, version."""
import struct, sys
data = open(sys.argv[1], "rb").read()
pe = struct.unpack_from("<I", data, 0x3C)[0]
assert data[pe:pe+4] == b"PE\0\0"
machine, nsec, _, _, _, optsz, _ = struct.unpack_from("<HHIIIHH", data, pe + 4)
opt = pe + 24
magic = struct.unpack_from("<H", data, opt)[0]
assert magic == 0x20B, "not PE32+"
subsystem = struct.unpack_from("<H", data, opt + 68)[0]
rsrc_rva, rsrc_size = struct.unpack_from("<II", data, opt + 112 + 8 * 2)
secs = []
for i in range(nsec):
    s = opt + optsz + 40 * i
    name = data[s:s+8].rstrip(b"\0").decode()
    vsize, va, rawsize, rawptr = struct.unpack_from("<IIII", data, s + 8)
    secs.append((name, va, vsize, rawptr, rawsize))
def off(rva):
    for n, va, vs, rp, rs in secs:
        if va <= rva < va + max(vs, rs):
            return rp + rva - va
    raise ValueError("rva %x not mapped" % rva)
base = off(rsrc_rva)
found = {}
def walk(at, depth, path):
    _, _, _, _, nn, ni = struct.unpack_from("<IIHHHH", data, base + at)
    for i in range(nn + ni):
        id_, to = struct.unpack_from("<II", data, base + at + 16 + 8 * i)
        if to & 0x80000000:
            walk(to & 0x7FFFFFFF, depth + 1, path + [id_])
        else:
            rva, size, _, _ = struct.unpack_from("<IIII", data, base + to)
            found[tuple(path + [id_])] = data[off(rva):off(rva) + size]
walk(0, 0, [])
types = sorted({k[0] for k in found})
print("machine=%x subsystem=%d (2=GUI) sections=%s" % (machine, subsystem, [s[0] for s in secs]))
print("resource types:", types, "count:", len(found))
grp = [v for k, v in found.items() if k[0] == 14][0]
cnt = struct.unpack_from("<H", grp, 4)[0]
icons = {k[1]: v for k, v in found.items() if k[0] == 3}
for i in range(cnt):
    w, h, _, _, _, bits, size, nid = struct.unpack_from("<BBBBHHIH", grp, 6 + 14 * i)
    assert nid in icons and len(icons[nid]) == size, ("icon", nid)
    assert icons[nid][:8] in (b"\x89PNG\r\n\x1a\n",) or icons[nid][:4] == b"\x28\0\0\0"
print("icon group ok:", cnt, "images")
man = [v for k, v in found.items() if k[0] == 24][0].decode()
assert "asInvoker" in man and "PerMonitorV2" in man
ver = [v for k, v in found.items() if k[0] == 16][0]
assert struct.unpack_from("<I", ver, 40)[0] == 0xFEEF04BD, "bad VS_FIXEDFILEINFO"
print("manifest ok; version strings:", [s for s in ver.decode("utf-16-le", "ignore").split("\0") if s][:20])
assert subsystem == 2
print("OK")

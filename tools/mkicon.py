"""Draws PlaySwap's icon: two overlapping player circles on a rounded tile."""
import sys
from PIL import Image, ImageDraw, ImageChops

def draw(size):
    S = 1024
    bg = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    d = ImageDraw.Draw(bg)
    d.rounded_rectangle((40, 40, S - 40, S - 40), radius=230, fill=(28, 34, 48, 255))
    r = 268
    cy = S // 2
    c1, c2 = (S // 2 - 150, cy), (S // 2 + 150, cy)
    blue = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    ImageDraw.Draw(blue).ellipse((c1[0] - r, cy - r, c1[0] + r, cy + r), fill=(79, 140, 255, 255))
    pink = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    ImageDraw.Draw(pink).ellipse((c2[0] - r, cy - r, c2[0] + r, cy + r), fill=(255, 93, 143, 255))
    # overlap: screen blend of the two, so the shared area glows
    ov_mask = ImageChops.multiply(blue.split()[3], pink.split()[3])
    screen = ImageChops.screen(blue.convert("RGB"), pink.convert("RGB"))
    out = bg.copy()
    out.alpha_composite(blue)
    out.alpha_composite(pink)
    glow = Image.new("RGBA", (S, S))
    glow.paste(screen, (0, 0))
    glow.putalpha(ov_mask)
    out.alpha_composite(glow)
    return out.resize((size, size), Image.LANCZOS)

if __name__ == "__main__":
    out = sys.argv[1]
    sizes = [16, 20, 24, 32, 40, 48, 64, 128, 256]
    imgs = [draw(s) for s in sizes]
    imgs[-1].save(out, format="ICO", sizes=[(s, s) for s in sizes], append_images=imgs[:-1])
    draw(512).save(out.replace(".ico", ".png"))

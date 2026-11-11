"""Sync checks, scripted with two browsers acting as two devices on the same household.

Needs the stack running (test profile is enough) and Playwright with a local Chrome:

    python3 test/sync/sync_test.py

Both "devices" load the gateway's Lampa page with the project's plugin, so bookmarks and timecodes go through
Lampac exactly as on real devices. Uses real TMDB ids; touches only this household's Lampac data (the cards
below are bookmarked and then removed again). Never prints a secret.
"""
import json
import os
import sys
import time

from playwright.sync_api import sync_playwright

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "canary"))
from canary_test import BASE, TOKEN  # noqa: E402  (same .env reading)

MATRIX = {"id": 603, "title": "Матрица", "original_title": "The Matrix", "release_date": "1999-03-30", "source": "tmdb"}
INCEPTION = {"id": 27205, "title": "Начало", "original_title": "Inception", "release_date": "2010-07-15", "source": "tmdb"}
INTERSTELLAR = {"id": 157336, "title": "Интерстеллар", "original_title": "Interstellar", "release_date": "2014-11-05",
                "source": "tmdb"}
DUNE_1984 = {"id": 841, "title": "Дюна", "original_title": "Dune", "release_date": "1984-12-14", "source": "tmdb"}
DUNE_2021 = {"id": 438631, "title": "Дюна", "original_title": "Dune", "release_date": "2021-09-15", "source": "tmdb"}
LIVE = 30  # seconds: sync check 1
failures = []


def check(name, ok, detail=""):
    print(("PASS" if ok else "FAIL"), name, ("- " + str(detail).replace(TOKEN, "<token>")) if not ok and detail else "")
    if not ok:
        failures.append(name)


def device(pw, profile=""):
    browser = pw.chromium.launch(channel="chrome", headless=True)
    ctx = browser.new_context()
    # A device that has been used once, past the "Who's watching?" screen for this session.
    # Init scripts run on every load: set up only the first one, so a profile switch survives the restart.
    ctx.add_init_script("try { if (!localStorage.getItem('ms_test_device')) { localStorage.setItem('ms_test_device', '1');"
                        " localStorage.setItem('language', 'ru'); localStorage.setItem('lampac_profile_id', %s);"
                        " sessionStorage.setItem('ms_profile_chosen', '1') } } catch (e) {}" % json.dumps(profile))
    page = ctx.new_page()
    page.goto(BASE + "/", timeout=120_000)
    ready(page)
    return browser, page


def ready(page):
    page.wait_for_function("() => window.media_stack_ready && window.Lampa && Lampa.Favorite && Lampa.Timeline",
                           timeout=120_000)
    page.wait_for_function("() => window.lwsEvent && window.lwsEvent.connectionId", timeout=30_000)
    page.wait_for_timeout(3_000)  # the sync plugins' first pull


def reload(page):
    page.reload()
    ready(page)


def bookmarked(page, card):
    return page.evaluate("(c) => !!Lampa.Favorite.check(c).book", card)


def until(fn, seconds=LIVE):
    end = time.time() + seconds
    while time.time() < end:
        if fn():
            return True
        time.sleep(1)
    return fn()


def bookmark(page, card, on=True):
    page.evaluate("([c, on]) => on ? Lampa.Favorite.add('book', c) : Lampa.Favorite.remove('book', c)", [card, on])


def watch(page, card, seconds):
    page.evaluate("""([c, t]) => Lampa.Timeline.update(Object.assign({hash: Lampa.Utils.hash(c.original_title),
        percent: Math.round(100 * t / 6000), time: t, duration: 6000}, Lampa.Timeline.link(c)))""", [card, seconds])


def position(page, card):
    return page.evaluate("(c) => Lampa.Timeline.view(Lampa.Utils.hash(c.original_title), Lampa.Timeline.link(c)).time",
                         card)


def profiles(page, body=None):
    return page.evaluate("""(b) => fetch(window.media_stack.base + '/profiles', b ? {method: 'POST', body: JSON.stringify(b)}
        : {}).then(r => r.json())""", body)


def pick(page, name):
    """Switch profile the way a person does: the top-bar profile icon, then the tile."""
    with page.expect_navigation(timeout=30_000):  # choosing another profile restarts Lampa
        page.evaluate("""(name) => new Promise((done) => {
        const icon = [...document.querySelectorAll('.head__action')].find(e => e.innerHTML.includes('M12 12a5'));
        $(icon).trigger('hover:enter');
        setTimeout(() => {
            const tile = [...document.querySelectorAll('.ms-profile')].find(e =>
                e.querySelector('.ms-profile__name').textContent === name);
            $(tile).trigger('hover:enter'); done();
        }, 1500);
    })""", name)
    ready(page)


def open_card(page, card):
    """Open the film's page, as a person does: Lampac's TimeCode plugin pulls positions for the open card."""
    page.evaluate("(c) => Lampa.Activity.push({url: '', component: 'full', id: c.id, method: 'movie', card: c, "
                  "source: 'tmdb'})", card)
    page.wait_for_timeout(4_000)


def main():
    with sync_playwright() as pw:
        (ba, a), (bb, b) = device(pw), device(pw)
        try:
            # 1. A bookmark on one device appears on the other within 30 s, and after its restart.
            bookmark(a, MATRIX)
            check("1 bookmark appears on the other device within 30 s", until(lambda: bookmarked(b, MATRIX)))
            reload(b)
            check("1 … and after the other device restarts", bookmarked(b, MATRIX))

            # 2. A position set on one device resumes on the other within ±10 s.
            watch(a, INCEPTION, 600)
            open_card(b, INCEPTION)
            ok = until(lambda: abs((position(b, INCEPTION) or 0) - 600) <= 10)
            check("2 resume position reaches the other device (±10 s)", ok, position(b, INCEPTION))
            reload(b)
            open_card(b, INCEPTION)
            check("2 … and after restart", abs((position(b, INCEPTION) or 0) - 600) <= 10, position(b, INCEPTION))

            # 3. Concurrent bookmarks on two devices both survive on both.
            bookmark(a, INCEPTION)
            bookmark(b, INTERSTELLAR)
            both = lambda p: bookmarked(p, INCEPTION) and bookmarked(p, INTERSTELLAR)
            check("3 concurrent bookmarks survive on both devices", until(lambda: both(a) and both(b)))

            # 5. Two films with the same original title keep separate positions.
            watch(a, DUNE_1984, 300)
            watch(a, DUNE_2021, 4200)
            reload(b)
            open_card(b, DUNE_1984)
            p84 = position(b, DUNE_1984)
            open_card(b, DUNE_2021)
            p21 = position(b, DUNE_2021)
            check("5 same original title, separate positions (other device)", p84 == 300 and p21 == 4200,
                  f"1984={p84} 2021={p21}")
            # Lampa keeps positions on the device by a hash of the original title, so the twins share one local
            # slot until a card opens and Lampac's plugin pulls that card's own position into it.
            open_card(a, DUNE_1984)
            q84 = position(a, DUNE_1984)
            open_card(a, DUNE_2021)
            q21 = position(a, DUNE_2021)
            check("5 … on the same device, each card opened", q84 == 300 and q21 == 4200, f"1984={q84} 2021={q21}")
            # 4. Two profiles on one device keep separate bookmarks and positions: switch on device A through the
            # "Who's watching?" screen and back.
            added = profiles(a, {"action": "add", "name": "sync-test"})
            test_id = next(p["id"] for p in added if p["name"] == "sync-test")
            pick(a, "sync-test")
            check("4 switch reaches the new profile", str(a.evaluate("Lampa.Storage.get('lampac_profile_id', '')")) == test_id)
            check("4 the new profile has none of the main profile's bookmarks", not bookmarked(a, MATRIX))
            open_card(a, INCEPTION)
            check("4 … nor its positions", (position(a, INCEPTION) or 0) == 0, position(a, INCEPTION))
            bookmark(a, INTERSTELLAR)  # this profile's own bookmark
            a.wait_for_timeout(2_000)
            pick(a, "Общий")
            check("4 back in the main profile its bookmarks return from the server",
                  until(lambda: bookmarked(a, MATRIX), 15))
            open_card(a, INCEPTION)
            check("4 … and its positions", abs((position(a, INCEPTION) or 0) - 600) <= 10, position(a, INCEPTION))
            profiles(a, {"action": "remove", "id": test_id})
        finally:
            for card in (MATRIX, INCEPTION, INTERSTELLAR):
                bookmark(a, card, False)
            ba.close(), bb.close()

    if failures:
        print(f"\n{len(failures)} failed: " + "; ".join(failures))
        sys.exit(1)
    print("\nall checks passed")


if __name__ == "__main__":
    main()

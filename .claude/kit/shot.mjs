#!/usr/bin/env node
// claude-kit UI snapshot: screenshots a page at desktop and mobile widths and reports, as text,
// what a reviewer needs: console errors, failed requests, horizontal overflow, small tap targets,
// and accessibility violations (when @axe-core/playwright is installed).
//
//   node .claude/kit/shot.mjs <url> [--name home] [--widths 1440,390] [--dark] [--full] [--wait "<css selector>"]
//
// Needs the project to have `playwright` (pnpm add -D -E playwright && pnpm exec playwright install chromium).
// Screenshots go to .claude/kit/logs/shots/<name>-<width>[-dark].png

import { mkdirSync } from 'node:fs';
import { dirname, join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

const args = process.argv.slice(2);
const url = args.find((a) => !a.startsWith('--') && !isFlagValue(a));
function isFlagValue(a) {
  const i = args.indexOf(a);
  return i > 0 && ['--name', '--widths', '--wait'].includes(args[i - 1]);
}
const opt = (k, d) => {
  const i = args.indexOf(k);
  return i >= 0 && args[i + 1] ? args[i + 1] : d;
};
if (!url) {
  console.error('usage: shot.mjs <url> [--name n] [--widths 1440,390] [--dark] [--full] [--wait selector]');
  process.exit(2);
}
const name = opt('--name', 'page').replace(/[^\w.-]+/g, '-');
const widths = opt('--widths', '1440,390').split(',').map(Number).filter(Boolean);
const dark = args.includes('--dark');
const full = args.includes('--full');
const waitFor = opt('--wait', '');

let chromium;
try {
  ({ chromium } = await import('playwright'));
} catch {
  console.error('shot.mjs: the project has no `playwright` package. Ask the user to run:\n' +
    '  pnpm add -D -E playwright && pnpm exec playwright install chromium   (or the npm/yarn/bun equivalent)');
  process.exit(3);
}
let AxeBuilder = null;
try {
  ({ default: AxeBuilder } = await import('@axe-core/playwright'));
} catch { /* optional */ }

const here = dirname(fileURLToPath(import.meta.url));
const outDir = join(here, 'logs', 'shots');
mkdirSync(outDir, { recursive: true });
const root = join(here, '..', '..');

let browser;
try {
  browser = await chromium.launch(process.env.CHROMIUM_PATH ? { executablePath: process.env.CHROMIUM_PATH } : {});
} catch (e) {
  console.error(`shot.mjs: could not start Chromium: ${String(e.message).split('\n')[0]}\n` +
    'Ask the user to run: pnpm exec playwright install chromium   (or set CHROMIUM_PATH to a Chrome/Chromium binary)');
  process.exit(3);
}
let problems = 0;
try {
  for (const width of widths) {
    const height = width < 600 ? 844 : 900;
    const context = await browser.newContext({
      viewport: { width, height },
      deviceScaleFactor: 1,
      colorScheme: dark ? 'dark' : 'light',
      // isMobile stays off: mobile emulation widens the layout viewport to fit content, hiding overflow
      hasTouch: width < 600,
    });
    const page = await context.newPage();
    const consoleErrors = [];
    const failed = [];
    page.on('console', (m) => m.type() === 'error' && consoleErrors.push(m.text().slice(0, 200)));
    page.on('pageerror', (e) => consoleErrors.push(String(e.message || e).slice(0, 200)));
    page.on('requestfailed', (r) => failed.push(`${r.failure()?.errorText ?? 'failed'} ${r.url().slice(0, 120)}`));
    page.on('response', (r) => r.status() >= 400 && failed.push(`${r.status()} ${r.url().slice(0, 120)}`));

    const res = await page.goto(url, { waitUntil: 'networkidle', timeout: 30000 }).catch((e) => ({ err: e }));
    if (res?.err) {
      console.log(`FAIL ${width}px: could not load ${url}: ${res.err.message.split('\n')[0]}`);
      problems++;
      await context.close();
      continue;
    }
    if (waitFor) await page.waitForSelector(waitFor, { timeout: 10000 }).catch(() => {});

    const file = join(outDir, `${name}-${width}${dark ? '-dark' : ''}.png`);
    await page.screenshot({ path: file, fullPage: full });

    const checks = await page.evaluate(() => {
      const doc = document.documentElement;
      const overflowX = doc.scrollWidth > window.innerWidth + 1;
      const wide = overflowX
        ? [...document.querySelectorAll('body *')]
            .filter((el) => el.getBoundingClientRect().right > window.innerWidth + 1)
            .slice(0, 5)
            .map((el) => el.tagName.toLowerCase() + (el.id ? '#' + el.id : '') +
              (typeof el.className === 'string' && el.className ? '.' + el.className.trim().split(/\s+/).slice(0, 2).join('.') : ''))
        : [];
      const small = [...document.querySelectorAll('a, button, input, select, textarea, [role=button]')]
        .filter((el) => {
          const r = el.getBoundingClientRect();
          return r.width > 0 && r.height > 0 && (r.width < 24 || r.height < 24);
        }).length;
      const imgsNoAlt = [...document.images].filter((i) => !i.hasAttribute('alt')).length;
      return { title: document.title, overflowX, wide, small, imgsNoAlt, h1: document.querySelectorAll('h1').length };
    });

    let axe = null;
    if (AxeBuilder) {
      const r = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze();
      axe = r.violations.map((v) => `${v.impact ?? '?'} ${v.id}: ${v.help} (${v.nodes.length})`);
    }

    const issues = [];
    if (checks.overflowX) issues.push(`horizontal overflow (widest: ${checks.wide.join(', ')})`);
    if (width < 600 && checks.small) issues.push(`${checks.small} tap targets under 24px`);
    if (checks.imgsNoAlt) issues.push(`${checks.imgsNoAlt} <img> without alt`);
    if (checks.h1 !== 1) issues.push(`${checks.h1} <h1> elements (expected 1)`);
    if (consoleErrors.length) issues.push(`${consoleErrors.length} console error(s)`);
    if (failed.length) issues.push(`${failed.length} failed request(s)`);
    if (axe?.length) issues.push(`${axe.length} axe violation(s)`);
    problems += issues.length;

    console.log(`${issues.length ? 'WARN' : 'OK  '} ${width}px${dark ? ' dark' : ''} · "${checks.title}" · ${relative(root, file)}`);
    for (const i of issues) console.log(`  - ${i}`);
    for (const e of consoleErrors.slice(0, 5)) console.log(`    console: ${e}`);
    for (const f of failed.slice(0, 5)) console.log(`    request: ${f}`);
    for (const a of (axe ?? []).slice(0, 10)) console.log(`    axe: ${a}`);
    await context.close();
  }
  if (!AxeBuilder) console.log('note: install @axe-core/playwright for accessibility checks');
} finally {
  await browser.close();
}
process.exit(problems ? 1 : 0);

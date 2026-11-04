// The interface bundled in the Android app talking to the companion on the
// Wi-Fi: page served elsewhere (here a static server), companion address in
// #lan=..., token in #token=....
const { chromium } = require('playwright');
(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 390, height: 844 } });
  const errors = [];
  page.on('pageerror', (e) => errors.push(e.message));
  const lan = encodeURIComponent('http://127.0.0.1:8787');
  await page.goto(`http://127.0.0.1:8798/index.html#lan=${lan}&token=${process.env.TOKEN}`);
  await page.waitForSelector('.dot.ok:has-text("LoL")', { timeout: 15000 });
  await page.waitForSelector('img.avatar', { timeout: 10000 });
  const src = await page.getAttribute('img.avatar', 'src');
  if (!src.startsWith('http://127.0.0.1:8787/lcu/')) throw new Error('images should come from the companion: ' + src);
  await page.reload();
  await page.waitForSelector('.dot.ok:has-text("LoL")', { timeout: 15000 });
  console.log('bundled interface on Wi-Fi OK', errors);
  await browser.close();
  if (errors.length) process.exit(1);
})().catch((e) => { console.error('FAILED', e); process.exit(1); });

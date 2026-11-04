// Remote access test: the phone opens the remote access link and drives the
// mock client through the relay (Cloudflare relay with run-remote.sh, MQTT
// broker with run-mqtt.sh), end to end encrypted.
const { chromium } = require('playwright');
const OUT = process.env.OUT || '.';
const LINK = process.env.LINK;
(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: 390, height: 844 } });
  // Images come from Community Dragon in remote mode: not needed here.
  await ctx.route('https://raw.communitydragon.org/**', (r) => r.fulfill({ status: 204 }));
  const phone = await ctx.newPage();
  const errors = [];
  phone.on('pageerror', (e) => errors.push('pageerror: ' + e.message));
  const frames = [];
  phone.on('websocket', (ws) => ws.on('framesent', (f) => frames.push(String(f.payload))));
  await fetch('http://127.0.0.1:29999/mock/goto?state=none');
  await phone.goto(LINK);
  await phone.waitForSelector('text=Choisis un mode de jeu', { timeout: 15000 });
  await phone.click('.mode:has-text("ARAM")');
  await phone.waitForSelector('text=Trouver une partie');
  await phone.click('text=Trouver une partie');
  await phone.waitForSelector('text=Partie trouvée', { timeout: 15000 });
  // (accepted automatically when the auto-accept option is on)
  const accept = await phone.waitForSelector('button.accept', { timeout: 3000 }).catch(() => null);
  if (accept) await accept.click();
  await phone.waitForSelector('text=Banc', { timeout: 15000 });
  await phone.click('.tab:has-text("Runes")');
  await phone.waitForSelector('.page-item');
  await phone.click('.icon-btn');
  await phone.waitForSelector('text=à distance (chiffrée de bout en bout)');
  await phone.screenshot({ path: `${OUT}/remote-settings.png` });
  // Nothing readable went through the relay.
  // (app messages carry "type" and LCU paths; encrypted ones are base64)
  const leaked = frames.filter((f) => f.includes('lol-') || f.includes('"type"'));
  console.log('frames sent:', frames.length, 'plaintext frames:', leaked.length);
  console.log('ERRORS:', JSON.stringify(errors));
  await browser.close();
  if (errors.length || leaked.length || frames.length < 3) process.exit(1);
  console.log('remote OK');
})().catch((e) => { console.error('FAILED', e); process.exit(1); });

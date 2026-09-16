// Connects a real Puppeteer client to gocurlffi's CDP server and runs a small
// flow: new page, navigate, evaluate, screenshot. This is the acceptance check
// for the CDP server: it uses the same client that drives Chrome.
import puppeteer from 'puppeteer-core';

const endpoint = process.argv[2] || 'ws://127.0.0.1:9222';
const url = process.argv[3] || 'https://example.com/';

const browser = await puppeteer.connect({ browserWSEndpoint: endpoint });
const page = await browser.newPage();
await page.goto(url, { waitUntil: 'load', timeout: 30000 });
const title = await page.title();
const h1 = await page.evaluate(() => document.querySelector('h1')?.textContent ?? null);
const shot = await page.screenshot({ encoding: 'base64' });
console.log(JSON.stringify({ title, h1, screenshotBytes: shot.length }));
await page.close();
await browser.disconnect();

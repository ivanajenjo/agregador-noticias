import puppeteer, { Browser, Page } from "puppeteer";

export function getCapturas() {
  captElPais();
  captElMundo();
}

async function captElPais() {
  const browser: Browser = await puppeteer.launch();
  const page: Page = await browser.newPage();
  await page.setViewport({ width: 1280, height: 800 });
  await page.goto("https://www.elpais.com/");
  //Aceptar Cookies
  await page.click("#didomi-notice-agree-button");
  await page.screenshot({ path: `temp/elpais_${Date.now()}.png`, fullPage: true });
  //TO-DO Obtener el texto de los titulares
  const links = await page.evaluate(() =>
    Array.from(document.querySelectorAll('[class="c"]'), (a) =>
      a.getAttribute("href")
    )
  );
  console.log(links);
  //fin codigo copiado
  await browser.close();
}

async function captElMundo() {
  const browser: Browser = await puppeteer.launch();
  const page: Page = await browser.newPage();
  await page.setViewport({ width: 1280, height: 800 });
  await page.goto("https://www.elmundo.es/");
  //Aceptar cookies
  await page.click("#didomi-notice-agree-button");
  await page.screenshot({ path: `temp/elmundo_${Date.now()}.png`, fullPage: true });
  await browser.close();
}

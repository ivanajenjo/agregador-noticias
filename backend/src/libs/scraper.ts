import puppeteer, { Browser, Page } from "puppeteer";

export function getCapturas(){
    captElPais();
    captElMundo();
}

async function captElPais(){
    const browser: Browser = await puppeteer.launch();
    const page: Page = await browser.newPage();
    await page.setViewport({ width: 1280, height: 800 })
    await page.goto('https://www.elpais.com/');	
    //Aceptar Cookies
    //await page.click('#didomi-notice-agree-button');
    await page.screenshot({ path: 'temp/elpais.png', fullPage: true })
    await browser.close();
}

async function captElMundo(){
    const browser: Browser = await puppeteer.launch();
    const page: Page = await browser.newPage();
    await page.setViewport({ width: 1280, height: 800 })
    await page.goto('https://www.elmundo.es/');	
    await page.screenshot({ path: 'temp/elmundo.png', fullPage: true })
    await browser.close();    
}
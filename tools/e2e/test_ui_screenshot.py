# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

import asyncio

from playwright.async_api import async_playwright


async def run():
    async with async_playwright() as p:
        browser = await p.chromium.launch(headless=True)
        page = await browser.new_page()

        print("Navigating to XMC Dashboard...")
        await page.goto("http://localhost:8080/")

        print("Waiting 5 seconds...")
        await page.wait_for_timeout(5000)

        await page.screenshot(path="xmc_ui_screenshot2.png", full_page=True)
        print("Screenshot saved to xmc_ui_screenshot2.png.")
        await browser.close()


if __name__ == "__main__":
    asyncio.run(run())

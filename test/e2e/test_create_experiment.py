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
import sys

from playwright.async_api import async_playwright


async def main():
    async with async_playwright() as p:
        browser = await p.chromium.launch(headless=True)
        page = await browser.new_page()

        target_url = "http://localhost:8080/"
        print(f"Navigating to {target_url}", flush=True)

        # Navigate and wait for idle
        await page.goto(target_url, wait_until="networkidle")

        print("Clicking 'Sign in with Google'...", flush=True)
        await page.click("text='Sign in with Google'")

        # Wait for the table/dashboard to load
        print("Waiting for dashboard redirect and load...", flush=True)

        # Validate successful authorization by checking for the Search for experiments input
        print("Asserting presence of 'Search for experiments' input field...", flush=True)
        try:
            search_input = page.locator("input[data-testid='search-input-field']")
            # wait_for automatically polls the DOM until it appears
            await search_input.wait_for(state="visible", timeout=15000)
            print(
                "SUCCESS! The search input field was found, confirming full dashboard authorization.",
                flush=True,
            )
        except Exception:
            print(
                "FAILED to find the authenticated dashboard search element! Authentication may have failed.",
                flush=True,
            )
            await page.screenshot(path="xmc_ui_failure.png", full_page=True)
            sys.exit(1)

        print("Taking final verified screenshot.", flush=True)
        await page.screenshot(path="xmc_ui_success.png", full_page=True)
        await browser.close()


if __name__ == "__main__":
    asyncio.run(main())

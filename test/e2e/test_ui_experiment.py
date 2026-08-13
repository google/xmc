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


async def main():
    async with async_playwright() as p:
        browser = await p.chromium.launch(headless=True)
        page = await browser.new_page()

        target_url = "http://localhost:8080/"
        print(f"Navigating to {target_url}")
        await page.goto(target_url, wait_until="networkidle")

        print("Clicking 'Sign in with Google'...")
        try:
            sign_in = page.locator("button:has-text('Sign in with Google')")
            await sign_in.wait_for(state="visible", timeout=3000)
            await sign_in.click()
            print("Successfully clicked Sign In button.")
            await page.wait_for_load_state("networkidle")
        except Exception as e:
            print(
                "No 'Sign in with Google' button found, maybe already logged in or auth is disabled?"
            )
            print(e)

        print("Waiting for the DataTable rows to render...")
        await page.locator(".data-table-row-selector, mat-row").first.wait_for(
            state="visible", timeout=5000
        )

        screenshot_path = "xmc_ui_experiment_list_v3.png"
        await page.screenshot(path=screenshot_path, full_page=True)
        print(f"Screenshot saved to {screenshot_path}")

        await browser.close()


if __name__ == "__main__":
    asyncio.run(main())

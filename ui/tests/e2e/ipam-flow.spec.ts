import { expect, test, type Page } from '@playwright/test'
import { base, signIn } from '../../../../gateway/shell/tests/e2e/helpers'

// Quickstart §4 flow for the IPAM remote at the three reference widths.
// Needs a full platform; skips without operator credentials.
const password = process.env.E2E_OPERATOR_PASSWORD ?? ''
const email = process.env.E2E_OPERATOR_EMAIL ?? 'ops@example.org'
const viewports = [{ name: 'phone', width: 320, height: 640 }, { name: 'tablet', width: 768, height: 1024 }, { name: 'desktop', width: 1280, height: 800 }]

async function openNav(page: Page, group: string, entry: string): Promise<void> {
  const burger = page.getByRole('button', { name: 'Open navigation' })
  if (await burger.isVisible()) await burger.click()
  const g = page.getByTestId('nav-group-' + group)
  if ((await g.getAttribute('aria-expanded')) !== 'true') await g.click()
  await page.getByTestId('nav-' + group).filter({ hasText: entry }).first().click()
}

test.describe('ipam remote', () => {
  test.skip(!password, 'E2E_OPERATOR_PASSWORD not set')
  for (const vp of viewports) {
    test(`${vp.name}: subnet tree, address table, scans, power/KVM gated`, async ({ page }) => {
      await page.setViewportSize({ width: vp.width, height: vp.height })
      const violations: string[] = []
      await page.addInitScript(() => document.addEventListener('securitypolicyviolation', (e) => console.error('CSP:' + (e as SecurityPolicyViolationEvent).violatedDirective)))
      page.on('console', (m) => { if (m.text().startsWith('CSP:')) violations.push(m.text()) })
      await page.goto(base + '/')
      await signIn(page, email, password)
      await openNav(page, 'ipam', 'Subnets')
      await expect(page.locator('main h1')).toHaveText('Subnets')
      await expect(page.getByRole('tree')).toBeVisible()
      await openNav(page, 'ipam', 'IP Addresses')
      await expect(page.getByTestId('addresses-table')).toBeVisible()
      await page.getByRole('button', { name: 'Allocate', exact: true }).click()
      await page.getByRole('dialog').locator('input[data-field=hostname]').fill('bad host')
      await page.getByRole('dialog').getByRole('button', { name: 'Allocate', exact: true }).click()
      await expect(page.getByRole('dialog').getByRole('alert')).toContainText('host name')
      await page.keyboard.press('Escape')
      await openNav(page, 'ipam', 'Scans')
      await expect(page.getByTestId('scans-table')).toBeVisible()
      await openNav(page, 'ipam', 'Devices')
      const first = page.locator('[data-test^=device-row-]').first()
      if (await first.isVisible()) {
        await first.click()
        // Operators without power:control / kvm:access never see the tab; platform admins do.
        const oob = page.getByRole('tab', { name: 'Power / KVM' })
        if (await oob.isVisible()) {
          await oob.click()
          await expect(page.getByTestId('kvm-start')).toBeVisible()
        }
      }
      await openNav(page, 'ipam', 'Dashboard')
      await expect(page.locator('.stat-tile').first()).toBeVisible()
      expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(0)
      expect(await page.locator('main [style]').count()).toBe(0)
      expect(violations).toEqual([])
    })
  }
})

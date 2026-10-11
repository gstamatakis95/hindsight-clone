// Command multi-tenant shows how a SaaS product serves several customers from one memd. Each customer is a
// tenant (the x-tenant-id header): tenants never see each other's banks, each has its own default behaviour,
// and a bank can be stamped out from a template with its own mission, disposition, directives and mental models.
package main

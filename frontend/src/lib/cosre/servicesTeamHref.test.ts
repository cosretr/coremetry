import { describe, it, expect } from 'vitest';
import { servicesTeamHref } from './servicesTeamHref';
import { SERVICES_URL_SPEC } from '../servicesUrlSpec';

describe('servicesTeamHref', () => {
  it('owner ekseni ?ownerTeam=, sre ekseni ?sreTeam=', () => {
    expect(servicesTeamHref(' payments-core ')).toBe('/services?ownerTeam=payments-core');
    expect(servicesTeamHref('sre-a', 'sre')).toBe('/services?sreTeam=sre-a');
  });
  it('param adları /services URL sözleşmesiyle aynı', () => {
    expect(Object.keys(SERVICES_URL_SPEC)).toEqual(expect.arrayContaining(['ownerTeam', 'sreTeam']));
  });
});

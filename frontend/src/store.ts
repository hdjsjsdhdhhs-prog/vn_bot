import { Api, ApiError } from './api';
import type { Offer, Order, Subscription } from './api';
import { safeInvoice } from './telegram';
import type { InvoiceResult, WebApp } from './telegram';

export interface Resource<T> { data: T; loading: boolean; error?: unknown }
export class Store {
  subscription: Resource<Subscription | null> = { data: null, loading: true };
  offers: Resource<Offer[]> = { data: [], loading: true };
  recent: Resource<Order[]> = { data: [], loading: true };
  order: Order | null = null;
  busy = false;
  checking = false;
  error: unknown;
  unauthorized = false;
  paymentHint: InvoiceResult | 'opening' | null = null;
  private listeners = new Set<() => void>();
  private timer?: ReturnType<typeof setTimeout>;
  private attempts = 0;
  private stopped = false;
  private generation = 0;
  private checkingID?: string;
  private paymentUncertain = false;
  private refreshing = false;
  private resourceRequests = new WeakMap<object, symbol>();
  private intent?: { offer: string; key: string };
  constructor(readonly api: Api, private telegram?: WebApp, private storage?: Storage) {
    try {
      const saved: unknown = JSON.parse(storage?.getItem('miniapp-intent') ?? 'null');
      if (saved && typeof saved === 'object' && 'offer' in saved && 'key' in saved &&
          typeof saved.offer === 'string' && /^[a-f0-9]{32}$/.test(saved.offer) &&
          typeof saved.key === 'string' && /^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/.test(saved.key)) {
        this.intent = { offer: saved.offer, key: saved.key };
      }
    } catch { /* Storage is optional, never used as authority. */ }
  }
  subscribe(fn: () => void) { this.listeners.add(fn); return () => this.listeners.delete(fn); }
  private emit() { if (!this.stopped) this.listeners.forEach(fn => fn()); }
  private fail(error: unknown) {
    if (this.unauthorized) return; // Preserve the reopen instruction after concurrent failures.
    this.error = error;
    if (error instanceof ApiError && error.status === 401) {
      this.unauthorized = true;
      this.generation++;
      this.subscription.data = null;
      this.offers.data = [];
      this.recent.data = [];
      this.order = null;
      clearTimeout(this.timer);
    }
  }
  private async load<T>(resource: Resource<T>, read: () => Promise<T>) {
    const request = Symbol();
    this.resourceRequests.set(resource, request);
    resource.loading = true;
    resource.error = undefined;
    this.emit();
    try {
      const data = await read();
      if (this.resourceRequests.get(resource) === request && !this.unauthorized && !this.stopped) resource.data = data;
    } catch (error) {
      if (this.resourceRequests.get(resource) === request) { resource.error = error; this.fail(error); }
      else if (error instanceof ApiError && error.status === 401) this.fail(error);
    } finally {
      if (this.resourceRequests.get(resource) === request) resource.loading = false;
      this.emit();
    }
  }
  async refresh() {
    if (this.unauthorized || this.stopped || this.refreshing) return;
    this.refreshing = true;
    this.error = undefined;
    try {
      await Promise.all([
        this.load(this.subscription, () => this.api.subscription()),
        this.load(this.offers, async () => (await this.api.offers()).offers),
        this.load(this.recent, async () => (await this.api.recent()).orders),
      ]);
    } finally { this.refreshing = false; }
  }
  private remember(order: Order) {
    if (this.intent?.offer === order.offer_id) {
      this.intent = undefined;
      try { this.storage?.removeItem('miniapp-intent'); } catch { /* Optional storage. */ }
    }
    this.order = order;
    // An older history read must not overwrite this newer authoritative order.
    this.resourceRequests.delete(this.recent);
    this.recent.loading = false;
    this.recent.error = undefined;
    this.recent.data = [order, ...this.recent.data.filter(item => item.order_id !== order.order_id)].slice(0, 20);
  }
  async buy(offer: Offer): Promise<Order | null> {
    if (this.busy || this.unauthorized || offer.currency !== 'XTR') return null;
    this.busy = true; this.error = undefined; this.emit();
    try {
      // Preserve key after an ambiguous transport error. No automatic POST retry.
      if (this.intent && this.intent.offer !== offer.offer_id) throw new ApiError('unresolved_intent');
      const pending = this.recent.data.find(item => item.offer_id === offer.offer_id && item.status === 'pending');
      if (pending) { this.remember(pending); return pending; }
      this.intent ??= { offer: offer.offer_id, key: crypto.randomUUID() };
      try { this.storage?.setItem('miniapp-intent', JSON.stringify(this.intent)); } catch { /* Optional storage. */ }
      const order = await this.api.create(offer.offer_id, this.intent.key);
      if (this.unauthorized || this.stopped) return null;
      this.intent = undefined;
      try { this.storage?.removeItem('miniapp-intent'); } catch { /* Optional storage. */ }
      this.remember(order);
      return order;
    } catch (error) {
      // A definitive rejection did not create an order; allow a fresh selection.
      if (error instanceof ApiError && error.status >= 400 && error.status < 500 && ![408, 429].includes(error.status)) {
        this.intent = undefined;
        try { this.storage?.removeItem('miniapp-intent'); } catch { /* Optional storage. */ }
      }
      this.fail(error);
      if (error instanceof ApiError && error.code === 'purchase_pending') await this.load(this.recent, async () => (await this.api.recent()).orders);
      return null;
    } finally { this.busy = false; this.emit(); }
  }
  leaveOrder() {
    clearTimeout(this.timer);
    this.generation++;
    this.order = null;
    this.paymentHint = null;
    this.checkingID = undefined;
    this.checking = false;
  }
  async selectOrder(id: string) {
    if (this.unauthorized || (this.checking && this.checkingID === id)) return;
    this.leaveOrder();
    this.error = undefined;
    this.attempts = 0;
    await this.checkOrder(id);
  }
  async checkOrder(id = this.order?.order_id) {
    if (!id || (this.checking && this.checkingID === id) || this.unauthorized || this.stopped) return;
    this.checkingID = id;
    this.checking = true;
    this.error = undefined;
    const generation = this.generation;
    this.emit();
    try {
      const order = await this.api.order(id);
      if (generation !== this.generation || this.unauthorized || this.stopped) return;
      this.remember(order);
      this.paymentUncertain = false;
      if (order.status === 'paid') {
        await this.load(this.subscription, () => this.api.subscription());
        await this.load(this.offers, async () => (await this.api.offers()).offers);
      }
    } catch (error) { if (generation === this.generation) this.fail(error); }
    finally {
      if (generation === this.generation && this.checkingID === id) {
        this.checkingID = undefined;
        this.checking = false;
        this.emit();
        this.schedule();
      }
    }
  }
  private schedule() {
    clearTimeout(this.timer);
    // Expired approved payments may still settle; do not infer failed payment.
    if (!this.stopped && !this.unauthorized && this.attempts < 24 && !document.hidden && this.order &&
        (this.order.status === 'pending' || (this.order.status === 'expired' && this.order.checkout_started))) {
      this.timer = setTimeout(() => { this.attempts++; void this.checkOrder(); }, Math.min(3000 + this.attempts * 1000, 10000));
    }
  }
  resume() { if (!document.hidden) { this.attempts = 0; void this.checkOrder(); } else clearTimeout(this.timer); }
  async pay() {
    const order = this.order;
    if (!order || this.busy || this.checking || this.unauthorized || order.status !== 'pending' || order.checkout_started || order.currency !== 'XTR') return;
    if (this.paymentUncertain) { await this.checkOrder(); return; }
    if (!this.telegram?.isVersionAtLeast('6.1')) { this.fail(new Error('Unsupported Telegram')); this.emit(); return; }
    const generation = this.generation;
    this.busy = true; this.error = undefined; this.paymentHint = 'opening'; this.emit();
    try {
      const invoice = await this.api.invoice(order.order_id);
      if (this.unauthorized || this.stopped || generation !== this.generation) {
        this.busy = false; this.emit(); return;
      }
      this.paymentUncertain = true;
      this.telegram.openInvoice(safeInvoice(invoice.invoice_url), status => {
        if (this.stopped || this.unauthorized) return;
        this.busy = false;
        // SDK status belongs only to the invoice that was actually opened.
        // It is not authoritative, even when it says paid.
        if (generation === this.generation && this.order?.order_id === order.order_id) {
          this.paymentHint = status;
          this.attempts = 0;
          void this.checkOrder(order.order_id);
        } else void this.refresh();
        this.emit();
      });
    } catch (error) {
      this.busy = false;
      if (generation === this.generation) { this.paymentHint = null; this.fail(error); }
      else if (error instanceof ApiError && error.status === 401) this.fail(error);
      this.emit();
    }
  }
  dispose() { this.stopped = true; clearTimeout(this.timer); this.listeners.clear(); }
}

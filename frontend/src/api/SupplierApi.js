import BaseApi from './BaseApi';

class SupplierApi extends BaseApi {
  getDiagnostics = () => this.http('/v1/suppliers');

  check = () => this.http('/v1/suppliers/check', {}, 'POST');
}

export default new SupplierApi();

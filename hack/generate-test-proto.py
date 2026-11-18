#!/usr/bin/env python3
"""Generate independent SMD test servers without production registry collisions.
Wire field numbers and RPC method names are preserved. Fixture descriptor names
are private, so the SDK's canonical descriptors can coexist in integration tests.
"""
from pathlib import Path
import re
import subprocess

root=Path(__file__).resolve().parent.parent
for src,version,old,new in [
    ('rbln_smd_v1.proto','v1','rblnservices.v1','rblnservices.exportertest.v1'),
    ('rbln_services.proto','v0','rblnservices','rblnservices.exportertest.v0'),
]:
    text=(root/'api'/src).read_text()
    text=text.replace('package '+old+';','package '+new+';')
    text=re.sub(r'option go_package = "[^"]*";', 'option go_package = "github.com/rebellions-sw/rbln-metrics-exporter/internal/smdtest/wire/'+version+'";',text)
    text=re.sub(r'extend google.protobuf.MethodOptions\s*\{[^}]*\}', '', text, flags=re.S)
    text=re.sub(r'^\s*option \(rpc_type\) = [^;]+;\n','',text,flags=re.M)
    text=text.replace('import "google/protobuf/descriptor.proto";','')
    source=root/'internal/smdtest/schema'/('exporter_test_smd_'+version+'.proto')
    source.write_text(text)
    output=root/'internal/smdtest/wire'/version
    subprocess.run(['protoc','-I',str(source.parent),'--go_out='+str(output),'--go_opt=paths=source_relative','--go-grpc_out='+str(output),'--go-grpc_opt=paths=source_relative',str(source)],check=True)
    rpc=output/(source.stem+'_grpc.pb.go')
    rpc.write_text(rpc.read_text().replace(new+'.',old+'.'))

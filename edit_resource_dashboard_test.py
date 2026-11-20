import re

with open('cmd/ui/app/resource_dashboard/resource_dashboard_test.ts', 'r') as f:
    content = f.read()

content = content.replace("resourceService.getQueueSecondaryData.and.returnValue(of({ queuePredictability: {} }));", "resourceService.getQueueSecondaryData.and.returnValue(of({ queuePredictability: {} }));\n    resourceService.getResourceAvailability = jasmine.createSpy().and.returnValue(of({ summaries: [] }));")
content = content.replace("      'getQueueJobs',", "      'getQueueJobs',\n      'getResourceAvailability',")

with open('cmd/ui/app/resource_dashboard/resource_dashboard_test.ts', 'w') as f:
    f.write(content)
